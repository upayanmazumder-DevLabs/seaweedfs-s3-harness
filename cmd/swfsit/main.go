// Command swfsit is the harness CLI. Today it provides one subcommand,
// janitor, which sweeps up leftover "it-*" buckets from interrupted test
// runs (aborting any in-flight multipart uploads first). The test suites
// under tests/ are run via `go test` directly; see the Makefile and README.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/s3x"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/target"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: swfsit janitor")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "janitor":
		if err := runJanitor(); err != nil {
			fmt.Fprintln(os.Stderr, "janitor:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

// runJanitor deletes every bucket prefixed "it-" (the harness's own
// isolation prefix; see internal/testkit.Bucket), aborting any in-flight
// multipart uploads first. It never touches a bucket outside that prefix.
func runJanitor() error {
	tg, err := target.FromEnv()
	if err != nil {
		return fmt.Errorf("loading target: %w", err)
	}
	cl := s3x.New(tg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	list, err := cl.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("ListBuckets: %w", err)
	}

	var swept, failed int
	for _, b := range list.Buckets {
		name := aws.ToString(b.Name)
		if !strings.HasPrefix(name, "it-") {
			continue
		}
		fmt.Printf("sweeping %s\n", name)
		if err := testkit.EmptyAndDeleteBucket(ctx, cl, name); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
			failed++
			continue
		}
		swept++
	}
	fmt.Printf("janitor: swept %d bucket(s), %d failure(s)\n", swept, failed)
	if failed > 0 {
		return fmt.Errorf("%d bucket(s) could not be fully cleaned up", failed)
	}
	return nil
}
