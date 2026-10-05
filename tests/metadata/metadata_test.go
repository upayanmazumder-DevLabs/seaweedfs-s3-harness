// Package metadata covers Content-Type, x-amz-meta-*, ETag/Last-Modified
// consistency, range edge cases, and conditional requests: matrix IDs
// MD01-MD11 in docs/test-matrix.md.
package metadata

import (
	"bytes"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/payload"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// MD01: Content-Type round-trips exactly, including a parameter.
func TestContentTypeRoundtrip(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "ct"

	const want = "application/zip; charset=binary"
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x")),
		ContentType: aws.String(want),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()
	if aws.ToString(out.ContentType) != want {
		t.Errorf("Content-Type: want %q got %q", want, aws.ToString(out.ContentType))
	}
}

// MD02: x-amz-meta-* keys and UTF-8 values round-trip on both GET and HEAD.
func TestUserMetadataRoundtrip(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "meta"

	meta := map[string]string{
		"task-id":   "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"submitter": "r\u00e9sum\u00e9-\u4e2d\u6587",
	}
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x")),
		Metadata: meta,
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	for _, fetch := range []string{"GET", "HEAD"} {
		t.Run(fetch, func(t *testing.T) {
			var got map[string]string
			if fetch == "GET" {
				out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
				if err != nil {
					t.Fatalf("GetObject: %v", err)
				}
				defer out.Body.Close()
				got = out.Metadata
			} else {
				out, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
				if err != nil {
					t.Fatalf("HeadObject: %v", err)
				}
				got = out.Metadata
			}
			for k, v := range meta {
				if got[k] != v {
					t.Errorf("metadata[%q]: want %q got %q", k, v, got[k])
				}
			}
		})
	}
}

// MD04: overwriting an object with different metadata must not leave any
// of the previous metadata keys behind.
func TestOverwriteReplacesMetadata(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "meta-overwrite"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("a")),
		Metadata: map[string]string{"old-key": "old-value"},
	}); err != nil {
		t.Fatalf("PutObject #1: %v", err)
	}
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("b")),
		Metadata: map[string]string{"new-key": "new-value"},
	}); err != nil {
		t.Fatalf("PutObject #2: %v", err)
	}
	out, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if _, present := out.Metadata["old-key"]; present {
		t.Error("old-key survived an overwrite that did not set it")
	}
	if out.Metadata["new-key"] != "new-value" {
		t.Errorf("new-key: want new-value got %q", out.Metadata["new-key"])
	}
}

// MD06: the ETag must be byte-identical (including quoting) across PUT,
// HEAD, GET and ListObjectsV2 for the same object version.
func TestETagConsistentAcrossOperations(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "etag-consistency"

	p := payload.New(1, 8192, payload.Random)
	put, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader()})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	head, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	get, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer get.Body.Close()
	list, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(key)})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	if len(list.Contents) != 1 {
		t.Fatalf("expected exactly 1 listed object, got %d", len(list.Contents))
	}

	want := aws.ToString(put.ETag)
	if want != p.ETag() {
		t.Errorf("PUT ETag: want %s got %s", p.ETag(), want)
	}
	if aws.ToString(head.ETag) != want {
		t.Errorf("HEAD ETag: want %s got %s", want, aws.ToString(head.ETag))
	}
	if aws.ToString(get.ETag) != want {
		t.Errorf("GET ETag: want %s got %s", want, aws.ToString(get.ETag))
	}
	if aws.ToString(list.Contents[0].ETag) != want {
		t.Errorf("List ETag: want %s got %s", want, aws.ToString(list.Contents[0].ETag))
	}
}

// MD08: range request edge cases: first byte, last byte, suffix range, an
// out-of-bounds suffix, and a start past the end of the object.
func TestRangeEdgeCases(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "range-edges"

	const size = 1000
	p := payload.New(2, size, payload.Cycle)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader()}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	t.Run("first_byte", func(t *testing.T) {
		out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=0-0")})
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		defer out.Body.Close()
		b := make([]byte, 1)
		if _, err := out.Body.Read(b); err != nil && err.Error() != "EOF" {
			t.Fatalf("reading: %v", err)
		}
		if b[0] != 0 {
			t.Errorf("byte 0: want 0 got %d", b[0])
		}
	})

	t.Run("suffix_last_10", func(t *testing.T) {
		out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=-10")})
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		defer out.Body.Close()
		if aws.ToInt64(out.ContentLength) != 10 {
			t.Errorf("Content-Length: want 10 got %d", aws.ToInt64(out.ContentLength))
		}
	})

	t.Run("suffix_larger_than_object", func(t *testing.T) {
		out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=-5000")})
		if err != nil {
			t.Fatalf("GetObject with an oversized suffix range should clamp to the whole object, got error: %v", err)
		}
		defer out.Body.Close()
		if aws.ToInt64(out.ContentLength) != size {
			t.Errorf("Content-Length: want %d (clamped) got %d", size, aws.ToInt64(out.ContentLength))
		}
	})

	t.Run("start_past_end", func(t *testing.T) {
		_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=5000-5010")})
		if err == nil {
			t.Fatal("GetObject with a start past the object's end succeeded")
		}
		if code := testkit.ErrorCode(err); code != "InvalidRange" {
			t.Errorf("error code: want InvalidRange got %q", code)
		}
	})
}

// MD10: conditional GET with If-None-Match against the current ETag must
// return 304; with a non-matching ETag it must return 200.
func TestConditionalGetIfNoneMatch(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "cond-get"

	put, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x"))})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	etag := aws.ToString(put.ETag)

	_, err = cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), IfNoneMatch: aws.String(etag)})
	if err == nil {
		t.Fatal("GetObject with a matching If-None-Match should fail with 304, got success")
	}
	if status := testkit.StatusCode(err); status != 304 {
		t.Errorf("status: want 304 got %d", status)
	}

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), IfNoneMatch: aws.String(`"not-the-real-etag"`)})
	if err != nil {
		t.Fatalf("GetObject with a non-matching If-None-Match should succeed: %v", err)
	}
	out.Body.Close()
}

// MD10: conditional GET/HEAD on a missing key must return 404, not 412 -
// regression coverage for github.com/seaweedfs/seaweedfs#10984.
func TestConditionalOnMissingKeyIs404NotPrecondition(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "never-existed"

	_, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), IfMatch: aws.String(`"anything"`)})
	if err == nil {
		t.Fatal("conditional HeadObject on a missing key succeeded")
	}
	if status := testkit.StatusCode(err); status != 404 {
		t.Errorf("conditional HeadObject on a missing key: want HTTP 404, got %d", status)
	}
}

// MD11: a conditional PUT with If-None-Match: * must fail with
// PreconditionFailed when the object already exists.
func TestConditionalPutIfNoneMatchStar(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "cond-put"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("first")),
		IfNoneMatch: aws.String("*"),
	}); err != nil {
		t.Fatalf("first conditional create: %v", err)
	}

	_, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("second")),
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		t.Fatal("second conditional create with If-None-Match:* succeeded against an existing key")
	}
	if status := testkit.StatusCode(err); status != 412 {
		t.Errorf("status: want 412 got %d", status)
	}
}

// MD07: Last-Modified must not move backward across an overwrite.
func TestLastModifiedMovesForward(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "metadata")
	key := testkit.Prefix(t) + "last-modified"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("a"))}); err != nil {
		t.Fatalf("PutObject #1: %v", err)
	}
	head1, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject #1: %v", err)
	}

	time.Sleep(1100 * time.Millisecond) // HTTP-date resolution is 1 second
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("b"))}); err != nil {
		t.Fatalf("PutObject #2: %v", err)
	}
	head2, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject #2: %v", err)
	}

	if !head2.LastModified.After(*head1.LastModified) {
		t.Errorf("Last-Modified did not move forward: first=%v second=%v", head1.LastModified, head2.LastModified)
	}
}
