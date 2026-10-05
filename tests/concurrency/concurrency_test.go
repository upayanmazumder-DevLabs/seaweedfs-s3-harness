// Package concurrency covers simultaneous readers/writers and race-prone
// operations: matrix IDs C01-C08 in docs/test-matrix.md. Every test checks
// an explicit invariant on the final state, not just "no errors".
package concurrency

import (
	"fmt"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/payload"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// C01: 32 concurrent PUTs to distinct keys must all verify independently.
func TestConcurrentPutsDistinctKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "concurrency")
	prefix := testkit.Prefix(t)

	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := payload.New(int64(i), 64*1024, payload.Random)
			key := fmt.Sprintf("%s%d", prefix, i)
			if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader()}); err != nil {
				errs[i] = fmt.Errorf("PutObject: %w", err)
				return
			}
			out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			if err != nil {
				errs[i] = fmt.Errorf("GetObject: %w", err)
				return
			}
			defer out.Body.Close()
			if err := p.VerifyReader(out.Body); err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// C02: 16 concurrent GETs of the same object must all return identical,
// correct bytes.
func TestConcurrentGetsSameObject(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "concurrency")
	key := testkit.Prefix(t) + "shared"

	p := payload.New(5, 2*1024*1024, payload.Random)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader()}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			if err != nil {
				errs[i] = err
				return
			}
			defer out.Body.Close()
			errs[i] = p.VerifyReader(out.Body)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// C04: a PUT racing a DELETE on the same key must leave the key in one of
// two valid final states - present with the PUT's bytes, or absent - never
// a partial or corrupted object.
func TestPutDeleteRace(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "concurrency")
	key := testkit.Prefix(t) + "race"

	seed := payload.New(8, 1024, payload.Random)
	p := payload.New(9, 1024, payload.Random)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: seed.Reader()}); err != nil {
		t.Fatalf("seed PutObject: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader()})
	}()
	go func() {
		defer wg.Done()
		_, _ = cl.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	}()
	wg.Wait()

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		if code := testkit.ErrorCode(err); code != "NoSuchKey" {
			t.Fatalf("after PUT/DELETE race: unexpected error (not NoSuchKey): %v", err)
		}
		return // absent is a valid outcome
	}
	defer out.Body.Close()
	if err := p.VerifyReader(out.Body); err != nil {
		t.Fatalf("after PUT/DELETE race: object present but corrupted (neither clean-absent nor the PUT's exact bytes): %v", err)
	}
}

// C05: N concurrent conditional creates (If-None-Match: *) on the same key
// must let exactly one succeed; every other must fail with 412.
// Regression coverage for github.com/seaweedfs/seaweedfs#8800.
func TestConcurrentConditionalCreateExactlyOneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "concurrency")
	key := testkit.Prefix(t) + "cond-create-race"

	const n = 16
	var wg sync.WaitGroup
	results := make([]int, n) // 0=unknown, 200=success, 412=precondition failed, -1=other error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := payload.New(int64(i), 256, payload.Random)
			_, err := cl.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
				IfNoneMatch: aws.String("*"),
			})
			if err == nil {
				results[i] = 200
				return
			}
			status := testkit.StatusCode(err)
			if status == 412 {
				results[i] = 412
			} else {
				results[i] = -1
			}
		}(i)
	}
	wg.Wait()

	wins, losses, other := 0, 0, 0
	for _, r := range results {
		switch r {
		case 200:
			wins++
		case 412:
			losses++
		default:
			other++
		}
	}
	if other > 0 {
		t.Errorf("%d goroutines got an error other than 412 Precondition Failed", other)
	}
	if wins != 1 {
		t.Errorf("expected exactly 1 winning conditional create, got %d (losses=%d)", wins, losses)
	}
}
