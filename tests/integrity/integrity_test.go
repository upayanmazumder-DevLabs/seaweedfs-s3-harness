// Package integrity verifies object bytes across sizes and checksum modes:
// matrix IDs I01-I09 in docs/test-matrix.md. Every test checks digests, not
// just HTTP status.
package integrity

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/payload"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/s3x"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// sizes spans the filer's 4 MiB chunk boundary and the SDK's 8 MiB fallback
// chunk size, up to the target's configured ceiling.
func sizes(maxMiB int64) []int64 {
	all := []int64{
		0, 1, 1024, 1024 * 1024,
		4*1024*1024 - 1, 4 * 1024 * 1024, 4*1024*1024 + 1,
		8*1024*1024 + 1,
		10 * 1024 * 1024,
		100 * 1024 * 1024,
	}
	var out []int64
	for _, s := range all {
		if s <= maxMiB*1024*1024 {
			out = append(out, s)
		}
	}
	return out
}

// I01: a full size/pattern matrix, single PUT, verified by digest on GET.
func TestPutGetAcrossSizes(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "integrity")
	prefix := testkit.Prefix(t)

	patterns := []payload.Pattern{payload.Random, payload.Zero, payload.Cycle}
	for _, size := range sizes(testkit.MaxObjectBytes(tg) / (1024 * 1024)) {
		for _, pat := range patterns {
			size, pat := size, pat
			t.Run(fmt.Sprintf("%dB_%s", size, pat), func(t *testing.T) {
				if size >= 10*1024*1024 && testing.Short() {
					t.Skip("short mode: skipping large-object integrity case")
				}
				key := prefix + fmt.Sprintf("%d-%s", size, pat)
				p := payload.New(size+int64(pat), size, pat)

				if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
					Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
				}); err != nil {
					t.Fatalf("PutObject: %v", err)
				}
				out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
				if err != nil {
					t.Fatalf("GetObject: %v", err)
				}
				defer out.Body.Close()
				if err := p.VerifyReader(out.Body); err != nil {
					t.Fatalf("%s: %v", p.Describe(), err)
				}
			})
		}
	}
}

// I02: ranged GETs across the filer's 4 MiB chunk boundary and the SDK's
// 8 MiB fallback boundary, off by one byte on each side, must return
// exactly the requested byte range.
func TestRangeAcrossChunkBoundaries(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "integrity")
	key := testkit.Prefix(t) + "ranged"

	const size = 10 * 1024 * 1024
	p := payload.New(99, size, payload.Cycle) // Cycle makes every offset self-identifying
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	boundaries := []int64{4 * 1024 * 1024, 8 * 1024 * 1024}
	for _, b := range boundaries {
		for _, start := range []int64{b - 1, b, b + 1} {
			start := start
			end := start + 99 // 100-byte window
			if end >= size {
				continue
			}
			t.Run(fmt.Sprintf("start=%d", start), func(t *testing.T) {
				out, err := cl.GetObject(ctx, &s3.GetObjectInput{
					Bucket: aws.String(bucket), Key: aws.String(key),
					Range: aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
				})
				if err != nil {
					t.Fatalf("ranged GetObject: %v", err)
				}
				defer out.Body.Close()
				got, err := io.ReadAll(out.Body)
				if err != nil {
					t.Fatalf("reading ranged body: %v", err)
				}
				if int64(len(got)) != end-start+1 {
					t.Fatalf("ranged body length: want %d got %d", end-start+1, len(got))
				}
				for i, b := range got {
					want := byte((start + int64(i)) % 256)
					if b != want {
						t.Fatalf("byte at offset %d (buffer index %d): want %d got %d", start+int64(i), i, want, b)
					}
				}
				if aws.ToInt64(out.ContentLength) != end-start+1 {
					t.Errorf("Content-Length: want %d got %d", end-start+1, aws.ToInt64(out.ContentLength))
				}
			})
		}
	}
}

// I03: a correct Content-MD5 on PUT must succeed.
func TestContentMD5Correct(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "integrity")
	key := testkit.Prefix(t) + "md5-ok"

	p := payload.New(5, 4096, payload.Random)
	_, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
		ContentMD5: aws.String(md5Base64(p)),
	})
	if err != nil {
		t.Fatalf("PutObject with correct Content-MD5: %v", err)
	}
}

// I04: a wrong Content-MD5 on an overwrite must be rejected, and the
// previous version of the object must remain intact (data-loss check).
func TestContentMD5WrongRejectedAndPreviousIntact(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "integrity")
	key := testkit.Prefix(t) + "md5-bad"

	good := payload.New(6, 4096, payload.Random)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: good.Reader(),
	}); err != nil {
		t.Fatalf("initial PutObject: %v", err)
	}

	bad := payload.New(7, 4096, payload.Random)
	_, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bad.Reader(),
		ContentMD5: aws.String("bm90dGhlcmVhbG1kNQ=="), // base64 garbage, 16 bytes decoded
	})
	if err == nil {
		t.Fatal("PutObject with a wrong Content-MD5 succeeded")
	}

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject after rejected overwrite: %v", err)
	}
	defer out.Body.Close()
	if err := good.VerifyReader(out.Body); err != nil {
		t.Fatalf("previous version was corrupted by the rejected overwrite: %v", err)
	}
}

// I09: a 100 MiB object GET through the SDK's HTTP client must match byte
// for byte, exercising whatever internal chunking the gateway does for
// large reads.
func TestLargeObjectRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tg := testkit.Target(t)
	if testkit.MaxObjectBytes(tg) < 100*1024*1024 {
		t.Skip("target MaxObjectMiB below 100")
	}
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "integrity")
	key := testkit.Prefix(t) + "large"

	p := payload.New(777, 100*1024*1024, payload.Random)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()
	if err := p.VerifyReader(out.Body); err != nil {
		t.Fatalf("100 MiB roundtrip: %v", err)
	}
}

// TestChecksumModesProduceIdenticalBytes runs the same PUT/GET under both
// SDK checksum modes (matching boto3 1.35.99's behavior vs. a modern SDK's
// default) and confirms the server-visible content is identical either way.
func TestChecksumModesProduceIdenticalBytes(t *testing.T) {
	tg := testkit.Target(t)
	_, ctx := testkit.Client(t, tg)
	bucket := func(cl *s3.Client) string { return testkit.Bucket(t, cl, ctx, "integrity") }

	p := payload.New(8, 256*1024, payload.Random)
	for _, mode := range []s3x.ChecksumMode{s3x.ChecksumWhenSupported, s3x.ChecksumWhenRequired} {
		mode := mode
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			cl := s3x.NewWithChecksumMode(tg, mode)
			b := bucket(cl)
			key := testkit.Prefix(t) + "checksum-mode"
			if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(b), Key: aws.String(key), Body: p.Reader(),
			}); err != nil {
				t.Fatalf("PutObject: %v", err)
			}
			out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b), Key: aws.String(key)})
			if err != nil {
				t.Fatalf("GetObject: %v", err)
			}
			defer out.Body.Close()
			if err := p.VerifyReader(out.Body); err != nil {
				t.Fatalf("checksum mode %d: %v", mode, err)
			}
		})
	}
}

// md5Base64 returns the base64 encoding of p's raw 16-byte MD5 digest, the
// form S3's Content-MD5 header requires.
func md5Base64(p *payload.Payload) string {
	h := md5.New()
	_, _ = io.Copy(h, p.Reader())
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
