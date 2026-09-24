package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func testProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"culipulse": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func fakeConfig(f *fakeapi.Server, body string) string {
	return fmt.Sprintf("provider \"culipulse\" {\n  endpoint  = %q\n  api_token = %q\n}\n", f.URL()+"/v1", fakeapi.Token) + body
}

func TestProvider_missingTokenIsAClearError(t *testing.T) {
	t.Setenv("CULIPULSE_API_TOKEN", "")
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      fmt.Sprintf("provider \"culipulse\" {\n  endpoint = %q\n}\ndata \"culipulse_agents\" \"all\" {}\n", f.URL()+"/v1"),
			ExpectError: regexp.MustCompile(`api_token is required`),
		}},
	})
}

func TestProvider_tokenAndEndpointFromEnv(t *testing.T) {
	f := fakeapi.New(t)
	t.Setenv("CULIPULSE_API_TOKEN", fakeapi.Token)
	t.Setenv("CULIPULSE_ENDPOINT", f.URL()+"/v1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: "data \"culipulse_agents\" \"all\" {}\n",
			Check:  resource.TestCheckResourceAttr("data.culipulse_agents.all", "ids.#", "3"),
		}},
	})
}

func TestProvider_badTokenSurfacesAPIError(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      fmt.Sprintf("provider \"culipulse\" {\n  endpoint  = %q\n  api_token = \"cpk_wrong\"\n}\ndata \"culipulse_agents\" \"all\" {}\n", f.URL()+"/v1"),
			ExpectError: regexp.MustCompile(`401.*request_id req_fake`),
		}},
	})
}
