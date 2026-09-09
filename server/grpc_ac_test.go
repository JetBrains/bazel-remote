package server

import (
	"context"
	"testing"

	pb "github.com/buchgr/bazel-remote/v2/genproto/build/bazel/remote/execution/v2"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestRequestMetadataTarget(t *testing.T) {
	blob, err := proto.Marshal(&pb.RequestMetadata{
		TargetId: "//platform/util:util",
	})
	if err != nil {
		t.Fatal(err)
	}

	// grpc-go base64-decodes "-bin" values on the wire, so an incoming
	// context holds the raw proto bytes.
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(requestMetadataKey, string(blob)))

	if targetID := requestMetadataTarget(ctx); targetID != "//platform/util:util" {
		t.Errorf("target: got %q, want %q", targetID, "//platform/util:util")
	}
}

// Clients need not send request metadata, and the HTTP protocol has no way
// to, so every one of these must yield "" rather than an error.
func TestRequestMetadataAbsentOrUnusable(t *testing.T) {
	cases := map[string]context.Context{
		"no metadata at all": context.Background(),
		"metadata without the key": metadata.NewIncomingContext(
			context.Background(), metadata.Pairs("other-key", "value")),
		"unparseable value": metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(requestMetadataKey, "\xff\xff not a proto")),
	}

	for name, ctx := range cases {
		t.Run(name, func(t *testing.T) {
			if targetID := requestMetadataTarget(ctx); targetID != "" {
				t.Errorf("got %q, want empty", targetID)
			}
		})
	}
}
