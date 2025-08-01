// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type AWSPollingConfig
package common

import (
	"fmt"
	"log"
	"os"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
)

// StateRefreshFunc is a function type used for StateChangeConf that is
// responsible for refreshing the item being watched for a state change.
//
// It returns three results. `result` is any object that will be returned
// as the final object after waiting for state change. This allows you to
// return the final updated object, for example an EC2 instance after refreshing
// it.
//
// `state` is the latest state of that object. And `err` is any error that
// may have happened while refreshing the state.
type StateRefreshFunc func() (result any, state string, err error)

// StateChangeConf is the configuration struct used for `WaitForState`.
type StateChangeConf struct {
	Pending   []string
	Refresh   StateRefreshFunc
	StepState multistep.StateBag
	Target    string
}

// Following are wrapper functions that use Packer's environment-variables to
// determine retry logic, then call the AWS SDK's built-in waiters.

// Polling configuration for the AWS waiter. Configures the waiter for resources creation or actions like attaching
// volumes or importing image.
//
// HCL2 example:
// ```hcl
//
//	aws_polling {
//		 delay_seconds = 30
//		 max_attempts = 50
//	}
//
// ```
//
// JSON example:
// ```json
//
//	"aws_polling" : {
//		 "delay_seconds": 30,
//		 "max_attempts": 50
//	}
//
// ```
type AWSPollingConfig struct {
	// Specifies the maximum number of attempts the waiter will check for resource state.
	// This value can also be set via the AWS_MAX_ATTEMPTS.
	// If both option and environment variable are set, the max_attempts will be considered over the AWS_MAX_ATTEMPTS.
	// If none is set, defaults to AWS waiter default which is 40 max_attempts.
	MaxAttempts int `mapstructure:"max_attempts" required:"false"`
	// Specifies the delay in seconds between attempts to check the resource state.
	// This value can also be set via the AWS_POLL_DELAY_SECONDS.
	// If both option and environment variable are set, the delay_seconds will be considered over the AWS_POLL_DELAY_SECONDS.
	// If none is set, defaults to AWS waiter default which is 15 seconds.
	DelaySeconds int `mapstructure:"delay_seconds" required:"false"`
}

// This helper function uses the environment variables AWS_TIMEOUT_SECONDS and
// AWS_POLL_DELAY_SECONDS to generate waiter options that can be passed into any
// request.Waiter function. These options will control how many times the waiter
// will retry the request, as well as how long to wait between the retries.

// DEFAULTING BEHAVIOR:
// if AWS_POLL_DELAY_SECONDS is set but the others are not, Packer will set this
// poll delay and use the waiter-specific default

// if AWS_TIMEOUT_SECONDS is set but AWS_MAX_ATTEMPTS is not, Packer will use
// AWS_TIMEOUT_SECONDS and _either_ AWS_POLL_DELAY_SECONDS _or_ 2 if the user has not set AWS_POLL_DELAY_SECONDS, to determine a max number of attempts to make.

// if AWS_TIMEOUT_SECONDS, _and_ AWS_MAX_ATTEMPTS are both set,
// AWS_TIMEOUT_SECONDS will be ignored.

// if AWS_MAX_ATTEMPTS is set but AWS_POLL_DELAY_SECONDS is not, then we will
// use waiter-specific defaults.

func (w *AWSPollingConfig) LogEnvOverrideWarnings() {
	pollDelayEnv := os.Getenv("AWS_POLL_DELAY_SECONDS")
	timeoutSecondsEnv := os.Getenv("AWS_TIMEOUT_SECONDS")
	maxAttemptsEnv := os.Getenv("AWS_MAX_ATTEMPTS")

	maxAttemptsIsSet := maxAttemptsEnv != "" || w.MaxAttempts != 0
	timeoutSecondsIsSet := timeoutSecondsEnv != ""
	pollDelayIsSet := pollDelayEnv != "" || w.DelaySeconds != 0

	if maxAttemptsIsSet && timeoutSecondsIsSet {
		warning := fmt.Sprintf("[WARNING] (aws): AWS_MAX_ATTEMPTS and " +
			"AWS_TIMEOUT_SECONDS are both set. Packer will use " +
			"AWS_MAX_ATTEMPTS and discard AWS_TIMEOUT_SECONDS.")
		if !pollDelayIsSet {
			warning = fmt.Sprintf("%s  Since you have not set the poll delay, "+
				"Packer will default to a 2-second delay.", warning)
		}
		log.Print(warning)
	} else if timeoutSecondsIsSet {
		log.Printf("[WARNING] (aws): env var AWS_TIMEOUT_SECONDS is " +
			"deprecated in favor of AWS_MAX_ATTEMPTS env or aws_polling_max_attempts config option. " +
			"If you have not explicitly set AWS_POLL_DELAY_SECONDS env or aws_polling_delay_seconds config option, " +
			"we are defaulting to a poll delay of 2 seconds, regardless of the AWS waiter's default.")
	}
	if !maxAttemptsIsSet && !timeoutSecondsIsSet && !pollDelayIsSet {
		log.Printf("[INFO] (aws): No AWS timeout and polling overrides have been set. " +
			"Packer will default to waiter-specific delays and timeouts. If you would " +
			"like to customize the length of time between retries and max " +
			"number of retries you may do so by setting the environment " +
			"variables AWS_POLL_DELAY_SECONDS and AWS_MAX_ATTEMPTS or the " +
			"configuration options aws_polling_delay_seconds and aws_polling_max_attempts " +
			"to your desired values.")
	}
}
