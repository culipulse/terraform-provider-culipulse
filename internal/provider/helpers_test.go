package provider

import (
	"strings"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestAddAPIWarnings_oneWarningPerServerWarningNeverAnError(t *testing.T) {
	var d diag.Diagnostics
	addAPIWarnings(&d, nil)
	if len(d) != 0 {
		t.Fatalf("no warnings in, got %d diagnostics", len(d))
	}
	addAPIWarnings(&d, []client.Warning{{Code: "own_agent_no_secrets", Message: "Your own agents never receive saved secrets."}})
	if len(d) != 1 || d.HasError() || d.WarningsCount() != 1 {
		t.Fatalf("diags = %+v", d)
	}
	if !strings.Contains(d[0].Detail(), "never receive saved secrets") {
		t.Fatalf("detail = %q", d[0].Detail())
	}
	if d[0].Summary() == "" {
		t.Fatal("empty summary")
	}
}
