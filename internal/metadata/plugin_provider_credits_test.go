package metadata

import (
	"context"
	"errors"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type creditsClient struct {
	fakePluginMetadataClient
	resp *pluginv1.GetPersonCreditsResponse
	err  error
	req  *pluginv1.GetPersonCreditsRequest
}

func (c *creditsClient) GetPersonCredits(_ context.Context, req *pluginv1.GetPersonCreditsRequest) (*pluginv1.GetPersonCreditsResponse, error) {
	c.req = req
	return c.resp, c.err
}

func creditsProvider(t *testing.T, client pluginMetadataClient) *PluginProvider {
	t.Helper()
	provider, err := NewPluginProviderWithClientFactory(map[string]string{
		pluginInstallationIDSetting: "1",
		capabilityIDSetting:         "tmdb",
	}, func(context.Context, int, string) (pluginMetadataClient, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestPluginProviderGetPersonCredits(t *testing.T) {
	ids, _ := structpb.NewStruct(map[string]any{"imdb": "tt0137523"})
	client := &creditsClient{resp: &pluginv1.GetPersonCreditsResponse{Credits: []*pluginv1.PersonCredit{{
		ItemType: "movie", ProviderId: "550", ProviderIds: ids, Title: "Fight Club", ReleaseDate: "1999-10-15",
		PosterPath: "tmdb://poster/fc.jpg", Department: "Acting", Character: "Tyler", EpisodeCount: 0, CreditId: "c1",
	}}}}
	credits, err := creditsProvider(t, client).GetPersonCredits(context.Background(), PersonDetailRequest{ProviderIDs: map[string]string{"tmdb": "287"}, Language: "en"})
	if err != nil || len(credits) != 1 {
		t.Fatalf("credits: %+v %v", credits, err)
	}
	c := credits[0]
	if c.Provider != "tmdb" || c.ProviderIDs["tmdb"] != "550" || c.ProviderIDs["imdb"] != "tt0137523" || c.PosterPath != "tmdb://poster/fc.jpg" || c.Character != "Tyler" {
		t.Fatalf("credit: %+v", c)
	}
	if client.req.GetProviderIds().GetFields()["tmdb"].GetStringValue() != "287" || client.req.GetLanguage() != "en" {
		t.Fatalf("request: %+v", client.req)
	}
}

func TestPluginProviderGetPersonCreditsUnsupported(t *testing.T) {
	// A client without the method, and a plugin that predates it.
	for name, client := range map[string]pluginMetadataClient{
		"no method":     &fakePluginMetadataClient{},
		"unimplemented": &creditsClient{err: status.Error(codes.Unimplemented, "unknown method")},
	} {
		if _, err := creditsProvider(t, client).GetPersonCredits(context.Background(), PersonDetailRequest{}); !errors.Is(err, ErrPersonCreditsUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := creditsProvider(t, &creditsClient{err: errors.New("boom")}).GetPersonCredits(context.Background(), PersonDetailRequest{}); err == nil || errors.Is(err, ErrPersonCreditsUnsupported) {
		t.Errorf("a real failure: %v", err)
	}
}
