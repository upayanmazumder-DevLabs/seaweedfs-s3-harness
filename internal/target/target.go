// Package target describes an S3-compatible endpoint under test and the
// capabilities it is known to support, so that tests can skip cleanly
// instead of failing when a target does not support a feature (for example
// AWS mode without a second IAM identity, or a reference target with no
// disruptor).
package target

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Credentials is one named identity's access key pair, optionally absent.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Present reports whether both key fields are non-empty.
func (c Credentials) Present() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// Capabilities records what a target is known to support, driving t.Skip
// decisions in tests rather than letting them fail on an irrelevant target.
type Capabilities struct {
	// Versioning indicates bucket versioning can be enabled.
	Versioning bool
	// MultiIdentity indicates ReadOnly/Scoped credentials are configured,
	// so authorization tests can run.
	MultiIdentity bool
	// Disruptor indicates a disrupt.Disruptor is available (k8s pod-kill
	// tests); false for compose and for AWS.
	Disruptor bool
	// MaxObjectMiB caps the largest object size integrity tests will
	// generate against this target, to keep CI time and cost bounded.
	MaxObjectMiB int64
}

// Target is one S3-compatible endpoint under test.
type Target struct {
	Name         string
	Endpoint     string
	Region       string
	PathStyle    bool
	Admin        Credentials
	ReadOnly     Credentials
	Scoped       Credentials
	ScopedBucket string // bucket the Scoped identity is allowed to touch
	Capabilities Capabilities
}

// FromEnv builds a Target from SWFSIT_* environment variables. This is the
// only supported configuration mechanism: there are no target files with
// embedded credentials, so nothing credential-shaped can be committed by
// accident.
//
// Required: SWFSIT_TARGET_NAME, SWFSIT_ENDPOINT, SWFSIT_ACCESS_KEY,
// SWFSIT_SECRET_KEY.
// Optional: SWFSIT_REGION (default us-east-1), SWFSIT_PATH_STYLE (default
// true), SWFSIT_RO_ACCESS_KEY/SWFSIT_RO_SECRET_KEY,
// SWFSIT_SCOPED_ACCESS_KEY/SWFSIT_SCOPED_SECRET_KEY/SWFSIT_SCOPED_BUCKET,
// SWFSIT_VERSIONING, SWFSIT_DISRUPTOR, SWFSIT_MAX_OBJECT_MIB.
func FromEnv() (*Target, error) {
	name := os.Getenv("SWFSIT_TARGET_NAME")
	endpoint := os.Getenv("SWFSIT_ENDPOINT")
	ak := os.Getenv("SWFSIT_ACCESS_KEY")
	sk := os.Getenv("SWFSIT_SECRET_KEY")

	var missing []string
	if name == "" {
		missing = append(missing, "SWFSIT_TARGET_NAME")
	}
	if endpoint == "" {
		missing = append(missing, "SWFSIT_ENDPOINT")
	}
	if ak == "" {
		missing = append(missing, "SWFSIT_ACCESS_KEY")
	}
	if sk == "" {
		missing = append(missing, "SWFSIT_SECRET_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("target: missing required env vars: %s", strings.Join(missing, ", "))
	}

	region := os.Getenv("SWFSIT_REGION")
	if region == "" {
		region = "us-east-1"
	}

	pathStyle := true
	if v := os.Getenv("SWFSIT_PATH_STYLE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("target: SWFSIT_PATH_STYLE: %w", err)
		}
		pathStyle = b
	}

	maxObjectMiB := int64(100)
	if v := os.Getenv("SWFSIT_MAX_OBJECT_MIB"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("target: SWFSIT_MAX_OBJECT_MIB: %w", err)
		}
		maxObjectMiB = n
	}

	t := &Target{
		Name:      name,
		Endpoint:  endpoint,
		Region:    region,
		PathStyle: pathStyle,
		Admin:     Credentials{AccessKeyID: ak, SecretAccessKey: sk},
		ReadOnly: Credentials{
			AccessKeyID:     os.Getenv("SWFSIT_RO_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("SWFSIT_RO_SECRET_KEY"),
		},
		Scoped: Credentials{
			AccessKeyID:     os.Getenv("SWFSIT_SCOPED_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("SWFSIT_SCOPED_SECRET_KEY"),
		},
		ScopedBucket: os.Getenv("SWFSIT_SCOPED_BUCKET"),
		Capabilities: Capabilities{
			Versioning:    boolEnv("SWFSIT_VERSIONING", true),
			Disruptor:     boolEnv("SWFSIT_DISRUPTOR", false),
			MaxObjectMiB:  maxObjectMiB,
			MultiIdentity: false,
		},
	}
	t.Capabilities.MultiIdentity = t.ReadOnly.Present() && t.Scoped.Present()
	return t, nil
}

func boolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
