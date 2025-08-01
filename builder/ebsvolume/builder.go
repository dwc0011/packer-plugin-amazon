// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type Config,BlockDevice

// The ebsvolume package contains a packersdk.Builder implementation that builds
// EBS volumes for Amazon EC2 using an ephemeral instance,
package ebsvolume

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/hashicorp/hcl/v2/hcldec"
	awscommon "github.com/hashicorp/packer-plugin-amazon/builder/common"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/multistep/commonsteps"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/packerbuilderdata"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

const BuilderId = "mitchellh.amazon.ebsvolume"

type Config struct {
	common.PackerConfig    `mapstructure:",squash"`
	awscommon.AccessConfig `mapstructure:",squash"`
	awscommon.RunConfig    `mapstructure:",squash"`

	// Enable enhanced networking (ENA but not SriovNetSupport) on
	// HVM-compatible AMIs. If set, add `ec2:ModifyInstanceAttribute` to your
	// AWS IAM policy. Note: you must make sure enhanced networking is enabled
	// on your instance. See [Amazon's documentation on enabling enhanced
	// networking](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/enhanced-networking.html#enabling_enhanced_networking).
	AMIENASupport config.Trilean `mapstructure:"ena_support" required:"false"`
	// Enable enhanced networking (SriovNetSupport but not ENA) on
	// HVM-compatible AMIs. If true, add `ec2:ModifyInstanceAttribute` to your
	// AWS IAM policy. Note: you must make sure enhanced networking is enabled
	// on your instance. See [Amazon's documentation on enabling enhanced
	// networking](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/enhanced-networking.html#enabling_enhanced_networking).
	// Default `false`.
	AMISriovNetSupport bool `mapstructure:"sriov_support" required:"false"`

	// Add the block device mappings to the AMI. If you add instance store
	// volumes or EBS volumes in addition to the root device volume, the
	// created AMI will contain block device mapping information for those
	// volumes. Amazon creates snapshots of the source instance's root volume
	// and any other EBS volumes described here. When you launch an instance
	// from this new AMI, the instance automatically launches with these
	// additional volumes, and will restore them from snapshots taken from the
	// source instance. See the [BlockDevices](#block-devices-configuration)
	// documentation for fields.
	VolumeMappings BlockDevices `mapstructure:"ebs_volumes" required:"false"`
	// Key/value pair tags to apply to the volumes of the instance that is
	// *launched* to create EBS Volumes. These tags will *not* appear in the
	// tags of the resulting EBS volumes unless they're duplicated under `tags`
	// in the `ebs_volumes` setting. This is a [template
	// engine](/packer/docs/templates/legacy_json_templates/engine), see [Build template
	// data](#build-template-data) for more information.
	//
	//  Note: The tags specified here will be *temporarily* applied to volumes
	// specified in `ebs_volumes` - but only while the instance is being
	// created. Packer will replace all tags on the volume with the tags
	// configured in the `ebs_volumes` section as soon as the instance is
	// reported as 'ready'.
	VolumeRunTags map[string]string `mapstructure:"run_volume_tags"`
	// Same as [`run_volume_tags`](#run_volume_tags) but defined as a singular
	// repeatable block containing a `key` and a `value` field. In HCL2 mode
	// the
	// [`dynamic_block`](/packer/docs/templates/hcl_templates/expressions#dynamic-blocks)
	// will allow you to create those programatically.
	VolumeRunTag config.KeyValues `mapstructure:"run_volume_tag"`

	launchBlockDevices BlockDevices

	ctx interpolate.Context
}

type Builder struct {
	config Config
	runner multistep.Runner
}

type EngineVarsTemplate struct {
	BuildRegion string
	SourceAMI   string
}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec { return b.config.FlatMapstructure().HCL2Spec() }

func (b *Builder) Prepare(raws ...interface{}) ([]string, []string, error) {
	b.config.ctx.Funcs = awscommon.TemplateFuncs
	// Create passthrough for {{ .BuildRegion }} and {{ .SourceAMI }} variables
	// so we can fill them in later
	b.config.ctx.Data = &EngineVarsTemplate{
		BuildRegion: `{{ .BuildRegion }}`,
		SourceAMI:   `{{ .SourceAMI }} `,
	}
	err := config.Decode(&b.config, &config.DecodeOpts{
		PluginType:         BuilderId,
		Interpolate:        true,
		InterpolateContext: &b.config.ctx,
		InterpolateFilter: &interpolate.RenderFilter{
			Exclude: []string{
				"fleet_tags",
				"fleet_tag",
				"run_tags",
				"run_tag",
				"run_volume_tags",
				"run_volume_tag",
				"spot_tags",
				"spot_tag",
				"tags",
				"tag",
			},
		},
	}, raws...)
	if err != nil {
		return nil, nil, err
	}

	// Accumulate any errors
	var errs *packersdk.MultiError
	var warns []string

	errs = packersdk.MultiErrorAppend(errs, b.config.VolumeRunTag.CopyOn(&b.config.VolumeRunTags)...)
	errs = packersdk.MultiErrorAppend(errs, b.config.AccessConfig.Prepare(&b.config.PackerConfig)...)
	errs = packersdk.MultiErrorAppend(errs, b.config.RunConfig.Prepare(&b.config.ctx)...)
	errs = packersdk.MultiErrorAppend(errs, b.config.launchBlockDevices.Prepare(&b.config.ctx)...)
	errs = packersdk.MultiErrorAppend(errs, b.config.VolumeMappings.Prepare(&b.config.ctx)...)

	b.config.launchBlockDevices = b.config.VolumeMappings
	if err != nil {
		errs = packersdk.MultiErrorAppend(errs, err)
	}

	if b.config.IsSpotInstance() && ((b.config.AMIENASupport.True()) || b.config.AMISriovNetSupport) {
		errs = packersdk.MultiErrorAppend(errs,
			fmt.Errorf("Spot instances do not support modification, which is required "+
				"when either `ena_support` or `sriov_support` are set. Please ensure "+
				"you use an AMI that already has either SR-IOV or ENA enabled."))
	}

	if b.config.RunConfig.SpotPriceAutoProduct != "" {
		warns = append(warns, "spot_price_auto_product is deprecated and no "+
			"longer necessary for Packer builds. In future versions of "+
			"Packer, inclusion of spot_price_auto_product will error your "+
			"builds. Please take a look at our current documentation to "+
			"understand how Packer requests Spot instances.")
	}

	if b.config.RunConfig.EnableT2Unlimited {
		warns = append(warns, "enable_t2_unlimited is deprecated please use "+
			"enable_unlimited_credits. In future versions of "+
			"Packer, inclusion of enable_t2_unlimited will error your builds.")
	}

	for _, configVolumeMapping := range b.config.VolumeMappings {
		if configVolumeMapping.SnapshotDescription != "" && !configVolumeMapping.SnapshotVolume {
			errs = packersdk.MultiErrorAppend(errs,
				fmt.Errorf("All `ebs_volumes` blocks setting `snapshot_description` must also set `snapshot_volume`."))
		}
	}

	if errs != nil && len(errs.Errors) > 0 {
		return nil, warns, errs
	}

	packersdk.LogSecretFilter.Set(b.config.AccessKey, b.config.SecretKey, b.config.Token)

	generatedData := awscommon.GetGeneratedDataList()
	return generatedData, warns, nil
}

func (b *Builder) Run(ctx context.Context, ui packersdk.Ui, hook packersdk.Hook) (packersdk.Artifact, error) {
	session, err := b.config.Session()
	if err != nil {
		return nil, err
	}
	ec2conn := ec2.New(session)
	iam := iam.New(session)

	// Setup the state bag and initial state for the steps
	state := new(multistep.BasicStateBag)
	state.Put("config", &b.config)
	state.Put("access_config", &b.config.AccessConfig)
	state.Put("ec2", ec2conn)
	state.Put("iam", iam)
	state.Put("hook", hook)
	state.Put("ui", ui)
	state.Put("region", ec2conn.Config.Region)
	generatedData := &packerbuilderdata.GeneratedData{State: state}

	var instanceStep multistep.Step

	if b.config.IsSpotInstance() {
		log.Printf("%s", "Using Spot Instance to create EBS volumes")
		instanceStep = &awscommon.StepRunSpotInstance{
			PollingConfig:                     b.config.PollingConfig,
			AssociatePublicIpAddress:          b.config.AssociatePublicIpAddress,
			LaunchMappings:                    b.config.launchBlockDevices,
			BlockDurationMinutes:              b.config.BlockDurationMinutes,
			Comm:                              &b.config.RunConfig.Comm,
			Ctx:                               b.config.ctx,
			Debug:                             b.config.PackerDebug,
			EbsOptimized:                      b.config.EbsOptimized,
			ExpectedRootDevice:                "ebs",
			IsBurstableInstanceType:           b.config.RunConfig.IsBurstableInstanceType(),
			EnableUnlimitedCredits:            b.config.EnableUnlimitedCredits,
			HttpEndpoint:                      b.config.Metadata.HttpEndpoint,
			HttpTokens:                        b.config.Metadata.HttpTokens,
			HttpPutResponseHopLimit:           b.config.Metadata.HttpPutResponseHopLimit,
			InstanceMetadataTags:              b.config.Metadata.InstanceMetadataTags,
			InstanceInitiatedShutdownBehavior: b.config.InstanceInitiatedShutdownBehavior,
			InstanceType:                      b.config.InstanceType,
			FleetTags:                         b.config.FleetTags,
			Region:                            *ec2conn.Config.Region,
			SourceAMI:                         b.config.SourceAmi,
			SpotInstanceTypes:                 b.config.SpotInstanceTypes,
			SpotAllocationStrategy:            b.config.SpotAllocationStrategy,
			SpotPrice:                         b.config.SpotPrice,
			SpotTags:                          b.config.SpotTags,
			Tags:                              b.config.RunTags,
			UserData:                          b.config.UserData,
			UserDataFile:                      b.config.UserDataFile,
			VolumeTags:                        b.config.VolumeRunTags,
		}
	} else {
		var tenancy string
		tenancies := []string{b.config.Placement.Tenancy, b.config.Tenancy}

		for i := range tenancies {
			if tenancies[i] != "" {
				tenancy = tenancies[i]
				break
			}
		}

		instanceStep = &awscommon.StepRunSourceInstance{
			PollingConfig:                     b.config.PollingConfig,
			AssociatePublicIpAddress:          b.config.AssociatePublicIpAddress,
			LaunchMappings:                    b.config.launchBlockDevices,
			CapacityReservationPreference:     b.config.CapacityReservationPreference,
			CapacityReservationId:             b.config.CapacityReservationId,
			CapacityReservationGroupArn:       b.config.CapacityReservationGroupArn,
			Comm:                              &b.config.RunConfig.Comm,
			Ctx:                               b.config.ctx,
			Debug:                             b.config.PackerDebug,
			EbsOptimized:                      b.config.EbsOptimized,
			EnableNitroEnclave:                b.config.EnableNitroEnclave,
			IsBurstableInstanceType:           b.config.RunConfig.IsBurstableInstanceType(),
			EnableUnlimitedCredits:            b.config.EnableUnlimitedCredits,
			ExpectedRootDevice:                "ebs",
			HttpEndpoint:                      b.config.Metadata.HttpEndpoint,
			HttpTokens:                        b.config.Metadata.HttpTokens,
			HttpPutResponseHopLimit:           b.config.Metadata.HttpPutResponseHopLimit,
			InstanceMetadataTags:              b.config.Metadata.InstanceMetadataTags,
			InstanceInitiatedShutdownBehavior: b.config.InstanceInitiatedShutdownBehavior,
			InstanceType:                      b.config.InstanceType,
			IsRestricted:                      b.config.IsChinaCloud(),
			SourceAMI:                         b.config.SourceAmi,
			Tags:                              b.config.RunTags,
			LicenseSpecifications:             b.config.LicenseSpecifications,
			HostResourceGroupArn:              b.config.Placement.HostResourceGroupArn,
			Tenancy:                           tenancy,
			UserData:                          b.config.UserData,
			UserDataFile:                      b.config.UserDataFile,
			VolumeTags:                        b.config.VolumeRunTags,
		}
	}

	// Build the steps
	steps := []multistep.Step{
		&awscommon.StepSourceAMIInfo{
			SourceAmi:                b.config.SourceAmi,
			EnableAMISriovNetSupport: b.config.AMISriovNetSupport,
			EnableAMIENASupport:      b.config.AMIENASupport,
			AmiFilters:               b.config.SourceAmiFilter,
		},
		&awscommon.StepNetworkInfo{
			VpcId:                    b.config.VpcId,
			VpcFilter:                b.config.VpcFilter,
			SecurityGroupIds:         b.config.SecurityGroupIds,
			SecurityGroupFilter:      b.config.SecurityGroupFilter,
			SubnetId:                 b.config.SubnetId,
			SubnetFilter:             b.config.SubnetFilter,
			AvailabilityZone:         b.config.AvailabilityZone,
			AssociatePublicIpAddress: b.config.AssociatePublicIpAddress,
			RequestedMachineType:     b.config.InstanceType,
		},
		&awscommon.StepKeyPair{
			Debug:        b.config.PackerDebug,
			Comm:         &b.config.RunConfig.Comm,
			IsRestricted: b.config.IsChinaCloud(),
			DebugKeyPath: fmt.Sprintf("ec2_%s.pem", b.config.PackerBuildName),
			Tags:         b.config.RunTags,
			Ctx:          b.config.ctx,
		},
		&awscommon.StepSecurityGroup{
			SecurityGroupFilter:       b.config.SecurityGroupFilter,
			SecurityGroupIds:          b.config.SecurityGroupIds,
			CommConfig:                &b.config.RunConfig.Comm,
			TemporarySGSourceCidrs:    b.config.TemporarySGSourceCidrs,
			TemporarySGSourcePublicIp: b.config.TemporarySGSourcePublicIp,
			SkipSSHRuleCreation:       b.config.SSMAgentEnabled(),
			IsRestricted:              b.config.IsChinaCloud(),
			Tags:                      b.config.RunTags,
			Ctx:                       b.config.ctx,
		},
		&awscommon.StepIamInstanceProfile{
			PollingConfig:                             b.config.PollingConfig,
			IamInstanceProfile:                        b.config.IamInstanceProfile,
			SkipProfileValidation:                     b.config.SkipProfileValidation,
			TemporaryIamInstanceProfilePolicyDocument: b.config.TemporaryIamInstanceProfilePolicyDocument,
			Tags: b.config.RunTags,
			Ctx:  b.config.ctx,
		},
		instanceStep,
		&stepTagEBSVolumes{
			VolumeMapping: b.config.VolumeMappings,
			Ctx:           b.config.ctx,
		},
		&awscommon.StepGetPassword{
			Debug:     b.config.PackerDebug,
			Comm:      &b.config.RunConfig.Comm,
			Timeout:   b.config.WindowsPasswordTimeout,
			BuildName: b.config.PackerBuildName,
		},
		&awscommon.StepCreateSSMTunnel{
			AWSSession:       session,
			Region:           *ec2conn.Config.Region,
			PauseBeforeSSM:   b.config.PauseBeforeSSM,
			LocalPortNumber:  b.config.SessionManagerPort,
			RemotePortNumber: b.config.Comm.Port(),
			SSMAgentEnabled:  b.config.SSMAgentEnabled(),
			SSHConfig:        &b.config.Comm.SSH,
		},
		&communicator.StepConnect{
			Config: &b.config.RunConfig.Comm,
			Host: awscommon.SSHHost(
				ec2conn,
				b.config.SSHInterface,
				b.config.Comm.Host(),
			),
			SSHPort: awscommon.Port(
				b.config.SSHInterface,
				b.config.Comm.Port(),
			),
			SSHConfig: b.config.RunConfig.Comm.SSHConfigFunc(),
		},
		&awscommon.StepSetGeneratedData{
			GeneratedData: generatedData,
		},
		&commonsteps.StepProvision{},
		&commonsteps.StepCleanupTempKeys{
			Comm: &b.config.RunConfig.Comm,
		},
		&awscommon.StepStopEBSBackedInstance{
			PollingConfig:       b.config.PollingConfig,
			Skip:                b.config.IsSpotInstance(),
			DisableStopInstance: b.config.DisableStopInstance,
		},
		&awscommon.StepModifyEBSBackedInstance{
			EnableAMISriovNetSupport: b.config.AMISriovNetSupport,
			EnableAMIENASupport:      b.config.AMIENASupport,
		},
		&stepSnapshotEBSVolumes{
			PollingConfig: b.config.PollingConfig,
			VolumeMapping: b.config.VolumeMappings,
			AccessConfig:  &b.config.AccessConfig,
			Ctx:           b.config.ctx,
		},
	}

	// Run!
	b.runner = commonsteps.NewRunner(steps, b.config.PackerConfig, ui)
	b.runner.Run(ctx, state)

	// If there was an error, return that
	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	// Build the artifact and return it
	artifact := &Artifact{
		Volumes:        state.Get("ebsvolumes").(EbsVolumes),
		Snapshots:      state.Get("ebssnapshots").(EbsSnapshots),
		BuilderIdValue: BuilderId,
		Conn:           ec2conn,
		StateData:      map[string]interface{}{"generated_data": state.Get("generated_data")},
	}
	ui.Say(fmt.Sprintf("Created Volumes: %s", artifact))
	return artifact, nil
}
