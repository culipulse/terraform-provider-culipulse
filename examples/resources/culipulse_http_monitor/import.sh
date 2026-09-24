# Import by monitor id (shown in the console URL). Secret headers, auth and a secret request
# body can't be read back: if CuliPulse holds any of these and your configuration doesn't set
# them, the next plan/apply prints a warning and then REMOVES them, since every apply sends
# the whole request setup. Add them to your configuration (secret_headers, bearer_token or
# basic_auth_*, request_body) before the first apply after import to keep them.
terraform import culipulse_http_monitor.api mon_3f2c9a1e-6b4d-4c8a-9f0e-1a2b3c4d5e6f
