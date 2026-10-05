// Package s3x builds aws-sdk-go-v2 S3 clients for a target.Target, with
// path-style addressing, static credentials and a transport suitable for a
// plain-HTTP in-cluster S3 gateway.
package s3x

import (
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/target"
)

// ChecksumMode selects whether the SDK computes and sends request checksums.
type ChecksumMode int

const (
	// ChecksumWhenSupported is the aws-sdk-go-v2 default since service/s3
	// v1.73 (Jan 2025): checksums are added whenever the operation
	// supports them. This is what VCAP would get from an SDK upgrade.
	ChecksumWhenSupported ChecksumMode = iota
	// ChecksumWhenRequired restricts automatic checksums to operations
	// that require one, matching pre-1.73 SDK behavior and boto3
	// 1.35.99 (VCAP's current pinned version), which never sends
	// x-amz-checksum-* headers of its own accord.
	ChecksumWhenRequired
)

// New builds an S3 client authenticated as the target's Admin identity,
// with the current SDK default checksum mode.
func New(t *target.Target) *s3.Client {
	return newWithCredentials(t, t.Admin.AccessKeyID, t.Admin.SecretAccessKey, ChecksumWhenSupported)
}

// NewWithChecksumMode builds an S3 client as Admin with an explicit checksum
// mode, so a test can run the same operation in both SDK modes and compare
// the bytes actually placed on the wire.
func NewWithChecksumMode(t *target.Target, mode ChecksumMode) *s3.Client {
	return newWithCredentials(t, t.Admin.AccessKeyID, t.Admin.SecretAccessKey, mode)
}

// NewReadOnly builds an S3 client authenticated as the target's ReadOnly
// identity. Callers must check target.Capabilities.MultiIdentity first.
func NewReadOnly(t *target.Target) *s3.Client {
	return newWithCredentials(t, t.ReadOnly.AccessKeyID, t.ReadOnly.SecretAccessKey, ChecksumWhenSupported)
}

// NewScoped builds an S3 client authenticated as the target's Scoped
// identity. Callers must check target.Capabilities.MultiIdentity first.
func NewScoped(t *target.Target) *s3.Client {
	return newWithCredentials(t, t.Scoped.AccessKeyID, t.Scoped.SecretAccessKey, ChecksumWhenSupported)
}

// NewWithBadSignature builds a client whose secret key is deliberately
// wrong, for SignatureDoesNotMatch / InvalidAccessKeyId tests.
func NewWithBadSignature(t *target.Target) *s3.Client {
	return newWithCredentials(t, t.Admin.AccessKeyID, t.Admin.SecretAccessKey+"-wrong", ChecksumWhenSupported)
}

// NewAnonymous builds a client with empty credentials, producing
// unsigned requests, for anonymous-access tests.
func NewAnonymous(t *target.Target) *s3.Client {
	return newWithCredentials(t, "", "", ChecksumWhenSupported)
}

func newWithCredentials(t *target.Target, ak, sk string, mode ChecksumMode) *s3.Client {
	tr := &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
	}

	opts := s3.Options{
		Region:                     t.Region,
		UsePathStyle:               t.PathStyle,
		BaseEndpoint:               aws.String(t.Endpoint),
		HTTPClient:                 &http.Client{Transport: tr, Timeout: 5 * time.Minute},
		RequestChecksumCalculation: checksumRequest(mode),
		ResponseChecksumValidation: checksumResponse(mode),
	}
	if ak != "" {
		opts.Credentials = credentials.NewStaticCredentialsProvider(ak, sk, "")
	} else {
		opts.Credentials = aws.AnonymousCredentials{}
	}

	return s3.New(opts)
}

func checksumRequest(mode ChecksumMode) aws.RequestChecksumCalculation {
	if mode == ChecksumWhenRequired {
		return aws.RequestChecksumCalculationWhenRequired
	}
	return aws.RequestChecksumCalculationWhenSupported
}

func checksumResponse(mode ChecksumMode) aws.ResponseChecksumValidation {
	if mode == ChecksumWhenRequired {
		return aws.ResponseChecksumValidationWhenRequired
	}
	return aws.ResponseChecksumValidationWhenSupported
}
