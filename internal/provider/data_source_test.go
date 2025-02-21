package provider

import (
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

const (
	// EnvTfAccExternalTimeoutTest is the name of the environment variable used
	// to enable the 20 minute timeout test. The environment variable can be
	// set to any value to enable the test.
	EnvTfAccExternalTimeoutTest = "TF_ACC_EXTERNAL_TIMEOUT_TEST"
)

const testDataSourceConfig_basic = `
resource "exec_persisted" "test" {
  program = ["%s", "cheese"]

  query = {
    value = "pizza"
  }
}

output "query_value" {
  value = "${exec_persisted.test.result["query_value"]}"
}

output "argument" {
  value = "${exec_persisted.test.result["argument"]}"
}
`

func protoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"exec": providerserver.NewProtocol6WithError(New()),
	}
}

func TestDataSource_basic(t *testing.T) {
	programPath, err := buildDataSourceTestProgram()
	if err != nil {
		t.Fatal(err)
		return
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testDataSourceConfig_basic, programPath),
				Check: func(s *terraform.State) error {
					_, ok := s.RootModule().Resources["exec_persisted.test"]
					if !ok {
						return fmt.Errorf("missing data resource")
					}

					outputs := s.RootModule().Outputs

					if outputs["argument"] == nil {
						return fmt.Errorf("missing 'argument' output")
					}
					if outputs["query_value"] == nil {
						return fmt.Errorf("missing 'query_value' output")
					}

					if outputs["argument"].Value != "cheese" {
						return fmt.Errorf(
							"'argument' output is %q; want 'cheese'",
							outputs["argument"].Value,
						)
					}
					if outputs["query_value"].Value != "pizza" {
						return fmt.Errorf(
							"'query_value' output is %q; want 'pizza'",
							outputs["query_value"].Value,
						)
					}

					return nil
				},
			},
		},
	})
}

const testDataSourceConfig_error = `
resource "exec_persisted" "test" {
  program = ["%s"]

  query = {
    fail = "true"
  }
}
`

func TestDataSource_error(t *testing.T) {
	programPath, err := buildDataSourceTestProgram()
	if err != nil {
		t.Fatal(err)
		return
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:      fmt.Sprintf(testDataSourceConfig_error, programPath),
				ExpectError: regexp.MustCompile("I was asked to fail"),
			},
		},
	})
}

// Reference: https://github.com/hashicorp/terraform-provider-external/issues/110
func TestDataSource_Program_OnlyEmptyString(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `
					resource "exec_persisted" "test" {
						program = [
							"", # e.g. a variable that became empty
						]
				
						query = {
							value = "valuetest"
						}
					}
				`,
				ExpectError: regexp.MustCompile(`External Program Missing`),
			},
		},
	})
}

// Reference: https://github.com/hashicorp/terraform-provider-external/issues/110
func TestDataSource_Program_PathAndEmptyString(t *testing.T) {
	programPath, err := buildDataSourceTestProgram()
	if err != nil {
		t.Fatal(err)
		return
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
					resource "exec_persisted" "test" {
						program = [
							%[1]q,
							"", # e.g. a variable that became empty
						]
				
						query = {
							value = "valuetest"
						}
					}
				`, programPath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("exec_persisted.test", "result.query_value", "valuetest"),
				),
			},
		},
	})
}

func buildDataSourceTestProgram() (string, error) {
	// We have a simple Go program that we use as a stub for testing.
	cmd := exec.Command(
		"go", "install",
		"github.com/repack-tech/terraform-provider-external/internal/provider/test-programs/tf-acc-external-data-source",
	)
	err := cmd.Run()

	if err != nil {
		return "", fmt.Errorf("failed to build test stub program: %s", err)
	}

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(os.Getenv("HOME") + "/go")
	}

	programPath := path.Join(
		filepath.SplitList(gopath)[0], "bin", "tf-acc-external-data-source",
	)
	return programPath, nil
}

// Reference: https://github.com/hashicorp/terraform-provider-external/issues/145
func TestDataSource_20MinuteTimeout(t *testing.T) {
	if os.Getenv(EnvTfAccExternalTimeoutTest) == "" {
		t.Skipf("Skipping this test since the %s environment variable is not set to any value. "+
			"This test requires 20 minutes to run, so it is disabled by default.",
			EnvTfAccExternalTimeoutTest,
		)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `
					resource "exec_persisted" "test" {
						program = ["sleep", "1205"] # over 20 minutes
					}
				`,
				// Not External Program Execution Failed / State: signal: killed
				ExpectError: regexp.MustCompile(`Unexpected External Program Results`),
			},
		},
	})
}
