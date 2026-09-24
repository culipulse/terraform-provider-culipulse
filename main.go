package main

import (
	"context"
	"flag"
	"log"

	"github.com/culipulse/terraform-provider-culipulse/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is set at release time: -ldflags "-X main.version=0.1.0".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/culipulse/culipulse",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
