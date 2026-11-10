package pluginv1

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A metadata provider built before GetPersonCredits existed embeds
// UnimplementedMetadataProviderServer, so it answers UNIMPLEMENTED, which
// hosts read as a provider without credits.
func TestEmbeddedMetadataProviderAnswersPersonCreditsUnimplemented(t *testing.T) {
	var server MetadataProviderServer = struct{ UnimplementedMetadataProviderServer }{}
	_, err := server.GetPersonCredits(context.Background(), &GetPersonCreditsRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("GetPersonCredits error = %v, want UNIMPLEMENTED", err)
	}
}
