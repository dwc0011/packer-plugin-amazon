// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc struct-markdown

package ebssurrogate

import (
	"errors"

	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

type RootBlockDevice struct {
	SourceDeviceName string `mapstructure:"source_device_name"`
	// The device name exposed to the instance (for
	// example, /dev/sdh or xvdh). Required for every device in the block
	// device mapping.
	DeviceName string `mapstructure:"device_name" required:"false"`
	// Indicates whether the EBS volume is
	// deleted on instance termination. Default false. NOTE: If this
	// value is not explicitly set to true and volumes are not cleaned up by
	// an alternative method, additional volumes will accumulate after every
	// build.
	DeleteOnTermination bool `mapstructure:"delete_on_termination" required:"false"`
	// The number of I/O operations per second (IOPS) that
	// the volume supports. See the documentation on
	// IOPs
	// for more information
	IOPS int64 `mapstructure:"iops" required:"false"`
	// The volume type. gp2 for General Purpose
	// (SSD) volumes, io1 for Provisioned IOPS (SSD) volumes, st1 for
	// Throughput Optimized HDD, sc1 for Cold HDD, and standard for
	// Magnetic volumes.
	VolumeType string `mapstructure:"volume_type" required:"false"`
	// The size of the volume, in GiB. Required if
	// not specifying a snapshot_id.
	VolumeSize int64 `mapstructure:"volume_size" required:"false"`
	//Whether to use the CreateImage or RegisterImage API when creating the AMI.
	//When set to `create`, CreateImage creates the image from the instance itself,
	//and inherits properties from the instance. When set to `register`, the image
	//is created from a snapshot of the specified EBS volume, and no properties
	//are inherited from the instance.
	//Ref: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateImage.html
	//     https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RegisterImage.html
	ImageMethod string `mapstructure:"image_method" required:"false"`
}

func (c *RootBlockDevice) Prepare(ctx *interpolate.Context) []error {
	var errs []error

	if c.SourceDeviceName == "" {
		errs = append(errs, errors.New("source_device_name for the root_device must be specified"))
	}

	if c.DeviceName == "" {
		errs = append(errs, errors.New("device_name for the root_device must be specified"))
	}

	if c.VolumeType == "gp2" && c.IOPS != 0 {
		errs = append(errs, errors.New("iops may not be specified for a gp2 volume"))
	}

	if c.IOPS < 0 {
		errs = append(errs, errors.New("iops must be greater than 0"))
	}

	if c.VolumeSize < 0 {
		errs = append(errs, errors.New("volume_size must be greater than 0"))
	}

	if c.ImageMethod == "" {
		c.ImageMethod = "register"
	} else if c.ImageMethod != "create" && c.ImageMethod != "register" {
		errs = append(errs, errors.New("image_method must be 'create', 'register' or an empty string' "))
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}
