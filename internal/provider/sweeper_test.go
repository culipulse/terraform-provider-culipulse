package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Sweepers delete anything named "tf-acc-*" left behind in the staging acceptance tenant by a
// crashed or interrupted acceptance run. envClient refuses any endpoint but staging.
//
// Run: set -a; source ~/.config/culipulse/tf-acc.env; set +a
//
//	go test ./internal/provider -v -sweep=staging -timeout 10m
func TestMain(m *testing.M) { resource.TestMain(m) }

func init() {
	resource.AddTestSweepers("culipulse_monitors", &resource.Sweeper{
		Name: "culipulse_monitors",
		F: func(string) error {
			c, err := envClient()
			if err != nil {
				return err
			}
			ms, err := c.ListMonitors(context.Background())
			if err != nil {
				return err
			}
			for _, m := range ms {
				if strings.HasPrefix(m.Name, "tf-acc-") {
					if err := c.DeleteMonitor(context.Background(), m.ID); err != nil {
						return err
					}
				}
			}
			return nil
		},
	})
	resource.AddTestSweepers("culipulse_channels", &resource.Sweeper{
		Name: "culipulse_channels",
		F: func(string) error {
			c, err := envClient()
			if err != nil {
				return err
			}
			chs, err := c.ListChannels(context.Background())
			if err != nil {
				return err
			}
			for _, ch := range chs {
				if strings.HasPrefix(ch.Name, "tf-acc-") {
					if err := c.DeleteChannel(context.Background(), ch.ID); err != nil {
						return err
					}
				}
			}
			return nil
		},
	})
}
