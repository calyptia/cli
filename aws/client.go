package aws

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"text/template"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	awstypes "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/calyptia/api/types"
	"github.com/calyptia/core-images-index/go-index"
)

const (
	userDataTemplate = `
CALYPTIA_CLOUD_PROJECT_TOKEN={{.ProjectToken}}
CALYPTIA_CLOUD_AGGREGATOR_NAME={{.CoreInstanceName}}
`
	DefaultRegionName        = "us-east-1"
	DefaultInstanceTypeName  = "t2.micro"
	DefaultSecurityGroupName = "calyptia-core"
)

type (
	Client interface {
		EnsureKeyPair(ctx context.Context, keyPairName string) (string, error)
		FindMatchingAMI(ctx context.Context, version string) (string, error)
		EnsureInstanceType(ctx context.Context, instanceTypeName string) (string, error)
		EnsureSubnet(ctx context.Context, subNetID string) (string, error)
		EnsureSecurityGroup(ctx context.Context, securityGroupName, vpcID string) (string, error)
		EnsureSecurityGroupIngressRules(ctx context.Context, securityGroupID string) error
		CreateUserdata(ctx context.Context, in *CreateUserDataParams) (string, error)
		CreateInstance(ctx context.Context, in *CreateInstanceParams) (CreatedInstance, error)
	}

	DefaultClient struct {
		Client
		client *ec2.Client
	}

	CreateInstanceParams struct {
		ImageID         string
		InstanceType    string
		UserData        string
		SecurityGroupID string
		SubnetID        string
		KeyPairName     string
	}

	CreatedInstance struct {
		types.MetadataAWS
	}
)

func (i *CreatedInstance) String() string {
	return fmt.Sprintf("instance-id: %s, instance-type: %s, privateIpv4: %s", i.EC2InstanceID, i.EC2InstanceType, i.PrivateIP)
}

func New(ctx context.Context, region, credentials, profileFile, profileName string) (*DefaultClient, error) {
	var opts []func(options *awsconfig.LoadOptions) error

	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	if credentials != "" {
		opts = append(opts, awsconfig.WithSharedCredentialsFiles([]string{credentials}))
	}

	if profileFile != "" {
		opts = append(opts, awsconfig.WithSharedConfigFiles([]string{profileFile}))
	}

	if profileName != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profileName))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &DefaultClient{
		client: ec2.NewFromConfig(cfg),
	}, nil
}

func (c *DefaultClient) FindMatchingAMI(ctx context.Context, version string) (string, error) {
	containerIndex, err := index.NewContainer()
	if err != nil {
		return "", err
	}

	if version != "" {
		coreImageTag, err := containerIndex.Match(ctx, version)
		if err != nil {
			return "", err
		}
		version = coreImageTag
	} else {
		// If no specific core instance version has been provided, use the latest published image.
		latest, err := containerIndex.Last(ctx)
		if err != nil {
			return "", err
		}
		version = latest
	}

	awsIndex, err := index.NewAWS()
	if err != nil {
		return "", err
	}

	imageID, err := awsIndex.Match(ctx, version)
	if err != nil {
		return "", err
	}

	return imageID, nil
}

func (c *DefaultClient) EnsureSubnet(ctx context.Context, subNetID string) (string, error) {
	var describeSubnetInput ec2.DescribeSubnetsInput

	// find the default subnet
	if subNetID == "" {
		describeSubnetInput = ec2.DescribeSubnetsInput{
			Filters: []awstypes.Filter{
				{
					Name:   aws.String("defaultForAz"),
					Values: []string{"true"},
				},
			},
		}
	} else {
		describeSubnetInput = ec2.DescribeSubnetsInput{
			Filters: []awstypes.Filter{
				{
					Name:   aws.String("subnet-id"),
					Values: []string{subNetID},
				},
			},
		}
	}

	subNets, err := c.client.DescribeSubnets(ctx, &describeSubnetInput)
	if err != nil {
		return "", err
	}

	if len(subNets.Subnets) == 0 {
		return "", ErrSubnetNotFound
	}

	return *subNets.Subnets[0].VpcId, nil
}

func (c *DefaultClient) EnsureSecurityGroupIngressRules(ctx context.Context, securityGroupID string) error {
	authorizeSecurityGroupIngress := &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(securityGroupID),
		IpPermissions: []awstypes.IpPermission{
			{
				FromPort: aws.Int32(0),
				// -1 means udp and tcp.
				IpProtocol: aws.String("-1"),
				IpRanges: []awstypes.IpRange{
					{
						CidrIp:      aws.String("0.0.0.0/0"),
						Description: aws.String("allow all tcp/udp traffic to this calyptia-core instance"),
					},
				},
				ToPort: aws.Int32(65535),
			},
		},
	}
	_, err := c.client.AuthorizeSecurityGroupIngress(ctx, authorizeSecurityGroupIngress)
	if !errorIsAlreadyExists(err) {
		return err
	}
	return nil
}

func (c *DefaultClient) EnsureSecurityGroup(ctx context.Context, securityGroupName, vpcID string) (string, error) {
	describeSecurityGroupsInput := &ec2.DescribeSecurityGroupsInput{
		GroupNames: []string{securityGroupName},
	}

	securityGroups, err := c.client.DescribeSecurityGroups(ctx, describeSecurityGroupsInput)
	if err != nil && !errorIsNotFound(err) {
		return "", err
	}

	if securityGroups != nil && len(securityGroups.SecurityGroups) > 0 {
		return *securityGroups.SecurityGroups[0].GroupId, nil
	}
	createSecurityGroupInput := &ec2.CreateSecurityGroupInput{
		Description: aws.String(securityGroupName),
		GroupName:   aws.String(securityGroupName),
		VpcId:       aws.String(vpcID),
	}
	securityGroup, err := c.client.CreateSecurityGroup(ctx, createSecurityGroupInput)
	if err != nil {
		return "", err
	}
	return *securityGroup.GroupId, nil
}

func (c *DefaultClient) EnsureInstanceType(ctx context.Context, instanceTypeName string) (string, error) {
	var out awstypes.InstanceType

	describeInstanceTypeInput := &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []awstypes.InstanceType{awstypes.InstanceType(instanceTypeName)},
	}

	instanceTypes, err := c.client.DescribeInstanceTypes(ctx, describeInstanceTypeInput)
	if err != nil {
		return string(out), err
	}

	if len(instanceTypes.InstanceTypes) == 0 {
		return string(out), fmt.Errorf("could not find any results for instance type %s", instanceTypeName)
	}

	out = instanceTypes.InstanceTypes[0].InstanceType
	return string(out), nil
}

type CreateUserDataParams struct {
	ProjectToken     string
	CoreInstanceName string
}

func (c *DefaultClient) CreateUserdata(_ context.Context, in *CreateUserDataParams) (string, error) {
	var out bytes.Buffer

	t, err := template.New("userdata").Parse(userDataTemplate)
	if err != nil {
		return "", err
	}

	err = t.Execute(&out, in)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

func (c *DefaultClient) CreateInstance(ctx context.Context, in *CreateInstanceParams) (CreatedInstance, error) {
	var out CreatedInstance

	runInstanceInput := &ec2.RunInstancesInput{
		MaxCount:         aws.Int32(1),
		MinCount:         aws.Int32(1),
		ImageId:          aws.String(in.ImageID),
		InstanceType:     awstypes.InstanceType(in.InstanceType),
		UserData:         aws.String(in.UserData),
		SecurityGroupIds: []string{in.SecurityGroupID},
		SubnetId:         aws.String(in.SubnetID),
		KeyName:          aws.String(in.KeyPairName),
	}

	instances, err := c.client.RunInstances(ctx, runInstanceInput)
	if err != nil {
		return out, err
	}

	instance := instances.Instances[0]
	out.EC2InstanceID = *instance.InstanceId
	out.EC2InstanceType = string(instance.InstanceType)
	out.AMIID = *instance.ImageId
	out.Hostname = *instance.PublicDnsName
	out.PrivateIP = *instance.PrivateIpAddress
	return out, nil
}

func (c *DefaultClient) EnsureKeyPair(ctx context.Context, keyPairName string) (string, error) {
	createKeyPairInput := &ec2.CreateKeyPairInput{
		KeyName: aws.String(keyPairName),
	}

	createKeyPair, err := c.client.CreateKeyPair(ctx, createKeyPairInput)
	if err != nil && errorIsAlreadyExists(err) {
		describeKeyPairInput := &ec2.DescribeKeyPairsInput{
			KeyNames: []string{keyPairName},
		}

		keyPairs, err := c.client.DescribeKeyPairs(ctx, describeKeyPairInput)
		if err != nil {
			return "", err
		}
		return *keyPairs.KeyPairs[0].KeyName, nil
	}

	if err != nil {
		return "", err
	}

	return *createKeyPair.KeyName, nil
}
