// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type DeregistrationProtectionOptions

package common

import (
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

// DeregistrationProtectionOptions lets users set AMI deregistration protection
//
// HCL2 example:
//
// ```hcl
//
//	source "amazon-ebs" "basic-example" {
//	  deregistration_protection {
//	    enabled = true
//	    with_cooldown = true
//	  }
//	}
//
// ```
//
// JSON Example:
//
// ```json
// "builders" [
//
//	{
//	  "type": "amazon-ebs",
//	  "deregistration_protection": {
//	    "enabled": true,
//	    "with_cooldown": true
//	  }
//	}
//
// ]
// ```
//
// [Protect an AMI from deregistration](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ami-deregistration-protection.html)
// When deregistration protection is enabled, the AMI cannot be deregistered.
// To allow the AMI to be deregistered, you must first disable deregistration protection.
type DeregistrationProtectionOptions struct {
	// Enable AMI deregistration protection.
	// To allow the AMI to be deregistered, you must first disable deregistration protection.
	Enabled bool `mapstructure:"enabled"`
	// When you turn on deregistration protection on an AMI, you have the option to include a 24-hour cooldown period.
	// This cooldown period is the time during which deregistration protection remains in effect after you turn it off.
	// During this cooldown period, the AMI can’t be deregistered.
	// When the cooldown period ends, the AMI can be deregistered.
	WithCooldown bool `mapstructure:"with_cooldown" required:"false"`
}

// AMIConfig is for common configuration related to creating AMIs.
type AMIConfig struct {
	// The name of the resulting AMI that will appear when managing AMIs in the
	// AWS console or via APIs. This must be unique. To help make this unique,
	// use a function like timestamp (see [template
	// engine](/packer/docs/templates/legacy_json_templates/engine) for more info).
	AMIName string `mapstructure:"ami_name" required:"true"`
	// The description to set for the resulting
	// AMI(s). By default this description is empty.  This is a
	// [template engine](/packer/docs/templates/legacy_json_templates/engine), see [Build template
	// data](#build-template-data) for more information.
	AMIDescription string `mapstructure:"ami_description" required:"false"`
	// The type of virtualization for the AMI
	// you are building. This option is required to register HVM images. Can be
	// paravirtual (default) or hvm.
	AMIVirtType string `mapstructure:"ami_virtualization_type" required:"false"`
	// A list of account IDs that have access to
	// launch the resulting AMI(s). By default no additional users other than the
	// user creating the AMI has permissions to launch it.
	AMIUsers []string `mapstructure:"ami_users" required:"false"`
	// A list of groups that have access to
	// launch the resulting AMI(s). By default no groups have permission to launch
	// the AMI. `all` will make the AMI publicly accessible.
	// AWS currently doesn't accept any value other than "all"
	AMIGroups []string `mapstructure:"ami_groups" required:"false"`
	// A list of Amazon Resource Names (ARN) of AWS Organizations that have access to
	// launch the resulting AMI(s). By default no organizations have permission to launch
	// the AMI.
	AMIOrgArns []string `mapstructure:"ami_org_arns" required:"false"`
	// A list of Amazon Resource Names (ARN) of AWS Organizations organizational units (OU) that have access to
	// launch the resulting AMI(s). By default no organizational units have permission to launch
	// the AMI.
	AMIOuArns []string `mapstructure:"ami_ou_arns" required:"false"`
	// A list of product codes to
	// associate with the AMI. By default no product codes are associated with the
	// AMI.
	AMIProductCodes []string `mapstructure:"ami_product_codes" required:"false"`
	// A list of regions to copy the AMI to.
	// Tags and attributes are copied along with the AMI. AMI copying takes time
	// depending on the size of the AMI, but will generally take many minutes.
	AMIRegions []string `mapstructure:"ami_regions" required:"false"`
	// Set to true if you want to skip
	// validation of the ami_regions configuration option. Default false.
	AMISkipRegionValidation bool `mapstructure:"skip_region_validation" required:"false"`
	// Key/value pair tags applied to the AMI. This is a [template
	// engine](/packer/docs/templates/legacy_json_templates/engine), see [Build template
	// data](#build-template-data) for more information.
	//
	// The builder no longer adds a "Name": "Packer Builder" entry to the tags.
	AMITags map[string]string `mapstructure:"tags" required:"false"`
	// Same as [`tags`](#tags) but defined as a singular repeatable block
	// containing a `key` and a `value` field. In HCL2 mode the
	// [`dynamic_block`](/packer/docs/templates/hcl_templates/expressions#dynamic-blocks)
	// will allow you to create those programatically.
	AMITag config.KeyValues `mapstructure:"tag" required:"false"`
	// Enable enhanced networking (ENA but not SriovNetSupport) on
	// HVM-compatible AMIs. If set, add `ec2:ModifyInstanceAttribute` to your
	// AWS IAM policy.
	//
	// Note: you must make sure enhanced networking is enabled on your
	// instance. See [Amazon's documentation on enabling enhanced
	// networking](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/enhanced-networking.html#enabling_enhanced_networking).
	AMIENASupport config.Trilean `mapstructure:"ena_support" required:"false"`
	// Enable enhanced networking (SriovNetSupport but not ENA) on
	// HVM-compatible AMIs. If true, add `ec2:ModifyInstanceAttribute` to your
	// AWS IAM policy. Note: you must make sure enhanced networking is enabled
	// on your instance. See [Amazon's documentation on enabling enhanced
	// networking](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/enhanced-networking.html#enabling_enhanced_networking).
	// Default `false`.
	AMISriovNetSupport bool `mapstructure:"sriov_support" required:"false"`
	// Force Packer to first deregister an existing
	// AMI if one with the same name already exists. Default false.
	AMIForceDeregister bool `mapstructure:"force_deregister" required:"false"`
	// Force Packer to delete snapshots
	// associated with AMIs, which have been deregistered by force_deregister.
	// Default false.
	AMIForceDeleteSnapshot bool `mapstructure:"force_delete_snapshot" required:"false"`
	// Whether or not to encrypt the resulting AMI when
	// copying a provisioned instance to an AMI. By default, Packer will keep
	// the encryption setting to what it was in the source image. Setting false
	// will result in an unencrypted image, and true will result in an encrypted
	// one.
	//
	// If you have used the `launch_block_device_mappings` to set an encryption
	// key and that key is the same as the one you want the image encrypted with
	// at the end, then you don't need to set this field; leaving it empty will
	// prevent an unnecessary extra copy step and save you some time.
	//
	// Please note that if you are using an account with the global "Always
	// encrypt new EBS volumes" option set to `true`, Packer will be unable to
	// override this setting, and the final image will be encrypted whether
	// you set this value or not.
	AMIEncryptBootVolume config.Trilean `mapstructure:"encrypt_boot" required:"false"`
	// ID, alias or ARN of the KMS key to use for AMI encryption. This
	// only applies to the main `region` -- any regions the AMI gets copied to
	// will be encrypted by the default EBS KMS key for that region,
	// unless you set region-specific keys in `region_kms_key_ids`.
	//
	// Set this value if you select `encrypt_boot`, but don't want to use the
	// region's default KMS key.
	//
	// If you have a custom kms key you'd like to apply to the launch volume,
	// and are only building in one region, it is more efficient to leave this
	// and `encrypt_boot` empty and to instead set the key id in the
	// launch_block_device_mappings (you can find an example below). This saves
	// potentially many minutes at the end of the build by preventing Packer
	// from having to copy and re-encrypt the image at the end of the build.
	//
	// For valid formats see *KmsKeyId* in the [AWS API docs -
	// CopyImage](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CopyImage.html).
	// This field is validated by Packer, when using an alias, you will have to
	// prefix `kms_key_id` with `alias/`.
	AMIKmsKeyId string `mapstructure:"kms_key_id" required:"false"`
	// regions to copy the ami to, along with the custom kms key id (alias or
	// arn) to use for encryption for that region. Keys must match the regions
	// provided in `ami_regions`. If you just want to encrypt using a default
	// ID, you can stick with `kms_key_id` and `ami_regions`. If you want a
	// region to be encrypted with that region's default key ID, you can use an
	// empty string `""` instead of a key id in this map. (e.g. `"us-east-1":
	// ""`) However, you cannot use default key IDs if you are using this in
	// conjunction with `snapshot_users` -- in that situation you must use
	// custom keys. For valid formats see *KmsKeyId* in the [AWS API docs -
	// CopyImage](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CopyImage.html).
	//
	// This option supercedes the `kms_key_id` option -- if you set both, and
	// they are different, Packer will respect the value in
	// `region_kms_key_ids` for your build region and silently disregard the
	// value provided in `kms_key_id`.
	AMIRegionKMSKeyIDs map[string]string `mapstructure:"region_kms_key_ids" required:"false"`
	// If true, Packer will not check whether an AMI with the `ami_name` exists
	// in the region it is building in. It will use an intermediary AMI name,
	// which it will not convert to an AMI in the build region. It will copy
	// the intermediary AMI into any regions provided in `ami_regions`, then
	// delete the intermediary AMI. Default `false`.
	AMISkipBuildRegion bool `mapstructure:"skip_save_build_region"`
	// Enforce version of the Instance Metadata Service on the built AMI.
	// Valid options are unset (legacy) and `v2.0`. See the documentation on
	// [IMDS](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html)
	// for more information. Defaults to legacy.
	AMIIMDSSupport string `mapstructure:"imds_support" required:"false"`
	// The date and time to deprecate the AMI, in UTC, in the following format: YYYY-MM-DDTHH:MM:SSZ.
	// If you specify a value for seconds, Amazon EC2 rounds the seconds to the nearest minute.
	// You can’t specify a date in the past. The upper limit for DeprecateAt is 10 years from now.
	DeprecationTime string `mapstructure:"deprecate_at"`

	SnapshotConfig `mapstructure:",squash"`

	// Enable AMI deregistration protection. See
	// [DeregistrationProtectionOptions](#deregistration-protection-options) below for more
	// details on all of the options available, and for a usage example.
	DeregistrationProtection DeregistrationProtectionOptions `mapstructure:"deregistration_protection" required:"false"`
}

func stringInSlice(s []string, searchstr string) bool {
	for _, item := range s {
		if item == searchstr {
			return true
		}
	}
	return false
}

func (c *AMIConfig) Prepare(accessConfig *AccessConfig, ctx *interpolate.Context) []error {
	var errs []error

	errs = append(errs, c.SnapshotTag.CopyOn(&c.SnapshotTags)...)
	errs = append(errs, c.AMITag.CopyOn(&c.AMITags)...)

	if c.AMIName == "" {
		errs = append(errs, fmt.Errorf("ami_name must be specified"))
	}

	// Make sure that if we have region_kms_key_ids defined,
	// the regions in region_kms_key_ids are also in ami_regions
	if len(c.AMIRegionKMSKeyIDs) > 0 {
		for kmsKeyRegion := range c.AMIRegionKMSKeyIDs {
			if !stringInSlice(c.AMIRegions, kmsKeyRegion) {
				errs = append(errs, fmt.Errorf("Region %s is in region_kms_key_ids but not in ami_regions", kmsKeyRegion))
			}
		}
	}

	errs = append(errs, c.prepareRegions(accessConfig)...)

	// Prevent sharing of default KMS key encrypted volumes with other aws users
	if len(c.AMIUsers) > 0 || len(c.AMIOrgArns) > 0 || len(c.AMIOuArns) > 0 {
		if len(c.AMIKmsKeyId) == 0 && len(c.AMIRegionKMSKeyIDs) == 0 && c.AMIEncryptBootVolume.True() {
			errs = append(errs, fmt.Errorf("Cannot share AMI encrypted with default KMS key"))
		}
		if len(c.AMIRegionKMSKeyIDs) > 0 {
			for _, kmsKey := range c.AMIRegionKMSKeyIDs {
				if len(kmsKey) == 0 {
					errs = append(errs, fmt.Errorf("Cannot share AMI encrypted with default KMS key for other regions"))
				}
			}
		}
	}

	kmsKeys := make([]string, 0)
	if len(c.AMIKmsKeyId) > 0 {
		kmsKeys = append(kmsKeys, c.AMIKmsKeyId)
	}
	if len(c.AMIRegionKMSKeyIDs) > 0 {
		for _, kmsKey := range c.AMIRegionKMSKeyIDs {
			if len(kmsKey) > 0 {
				kmsKeys = append(kmsKeys, kmsKey)
			}
		}
	}

	if len(kmsKeys) > 0 && !c.AMIEncryptBootVolume.True() {
		errs = append(errs, fmt.Errorf("If you have set either "+
			"region_kms_key_ids or kms_key_id, encrypt_boot must also be true."))

	}
	for _, kmsKey := range kmsKeys {
		if !ValidateKmsKey(kmsKey) {
			errs = append(errs, fmt.Errorf("%q is not a valid KMS Key Id.", kmsKey))
		}
	}

	if len(c.SnapshotUsers) > 0 {
		if len(c.AMIKmsKeyId) == 0 && len(c.AMIRegionKMSKeyIDs) == 0 && c.AMIEncryptBootVolume.True() {
			errs = append(errs, fmt.Errorf("Cannot share snapshot encrypted "+
				"with default KMS key, see https://www.packer.io/docs/builders/amazon-ebs#region_kms_key_ids for more information"))
		}
		if len(c.AMIRegionKMSKeyIDs) > 0 {
			for _, kmsKey := range c.AMIRegionKMSKeyIDs {
				if len(kmsKey) == 0 {
					errs = append(errs, fmt.Errorf("Cannot share snapshot encrypted with default KMS key"))
				}
			}
		}
	}

	if len(c.AMIName) < 3 || len(c.AMIName) > 128 {
		errs = append(errs, fmt.Errorf("ami_name must be between 3 and 128 characters long"))
	}

	if c.AMIName != templateCleanAMIName(c.AMIName) {
		errs = append(errs, fmt.Errorf("AMIName should only contain "+
			"alphanumeric characters, parentheses (()), square brackets ([]), spaces "+
			"( ), periods (.), slashes (/), dashes (-), single quotes ('), at-signs "+
			"(@), or underscores(_). You can use the `clean_resource_name` template "+
			"filter to automatically clean your ami name."))
	}

	if c.AMIIMDSSupport != "" && c.AMIIMDSSupport != string(types.ImdsSupportValuesV20) {
		errs = append(errs,
			fmt.Errorf(`The only valid imds_support values are %q or the empty string`,
				string(types.ImdsSupportValuesV20)),
		)
	}

	if c.DeprecationTime != "" {
		if _, err := time.Parse(time.RFC3339, c.DeprecationTime); err != nil {
			errs = append(errs, fmt.Errorf(
				"deprecate_at is not a valid time: %q. Expect time format: YYYY-MM-DDTHH:MM:SSZ",
				c.DeprecationTime))
		}
	}

	if c.DeregistrationProtection.WithCooldown {
		c.DeregistrationProtection.Enabled = true
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

func (c *AMIConfig) prepareRegions(accessConfig *AccessConfig) (errs []error) {
	if len(c.AMIRegions) > 0 {
		regionSet := make(map[string]struct{})
		regions := make([]string, 0, len(c.AMIRegions))

		for _, region := range c.AMIRegions {
			// If we already saw the region, then don't look again
			if _, ok := regionSet[region]; ok {
				continue
			}

			// Mark that we saw the region
			regionSet[region] = struct{}{}

			// Make sure that if we have region_kms_key_ids defined,
			// the regions in ami_regions are also in region_kms_key_ids
			if len(c.AMIRegionKMSKeyIDs) > 0 {
				if _, ok := c.AMIRegionKMSKeyIDs[region]; !ok {
					errs = append(errs, fmt.Errorf("Region %s is in ami_regions but not in region_kms_key_ids", region))
				}
			}
			if (accessConfig != nil) && (region == accessConfig.RawRegion) {
				// make sure we don't try to copy to the region we originally
				// create the AMI in.
				log.Printf("Cannot copy AMI to AWS session region '%s', deleting it from `ami_regions`.", region)
				continue
			}
			regions = append(regions, region)
		}

		c.AMIRegions = regions
	}
	return errs
}

// See https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CopyImage.html
func ValidateKmsKey(kmsKey string) (valid bool) {
	//Pattern for matching KMS Key ID for multi-region keys
	// https://docs.aws.amazon.com/kms/latest/developerguide/concepts.html#key-id-key-id
	// We tolerate dashes for mrk keys because they seem to exist.
	mrkKeyIdPattern := `mrk-[a-f0-9]+[a-f0-9-]+$`

	//Pattern for matching KMS Key ID for single-region keys
	kmsKeyIdPattern := `[a-f0-9]+[a-f0-9-]+$`
	aliasPattern := `alias/[a-zA-Z0-9:/_-]+$`

	// Check if kmsKey  is just the KeyId or Alias
	if regexp.MustCompile(fmt.Sprintf("^%s", kmsKeyIdPattern)).MatchString(kmsKey) {
		return true
	}
	if regexp.MustCompile(fmt.Sprintf("^%s", mrkKeyIdPattern)).MatchString(kmsKey) {
		return true
	}
	if regexp.MustCompile(fmt.Sprintf("^%s", aliasPattern)).MatchString(kmsKey) {
		return true
	}

	// Check if kmsKey is the full ARN
	kmsArnStartPattern := `^arn:aws(-[a-z]{2}(-gov)?)?:kms:([a-z]{2}-(gov-)?[a-z]+-\d{1})?:(\d{12}):`
	if regexp.MustCompile(fmt.Sprintf("%skey/%s", kmsArnStartPattern, kmsKeyIdPattern)).MatchString(kmsKey) {
		return true
	}
	if regexp.MustCompile(fmt.Sprintf("%skey/%s", kmsArnStartPattern, mrkKeyIdPattern)).MatchString(kmsKey) {
		return true
	}

	if regexp.MustCompile(fmt.Sprintf("%s%s", kmsArnStartPattern, aliasPattern)).MatchString(kmsKey) {
		return true
	}

	return false
}
