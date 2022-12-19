package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"os/exec"
	"runtime"
	"strings"
)

var (
	_ resource.Resource = (*programResource)(nil)
	//_ resource.ResourceWithImportState = (*programResource)(nil)
)

func NewExternalResource() resource.Resource {
	return &programResource{}
}

type programResource struct{}

func (r *programResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_persisted"
}

func (r *programResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The `external` data source allows an external program implementing a specific protocol " +
			"(defined below) to act as a data source, exposing arbitrary data for use elsewhere in the Terraform " +
			"configuration.\n" +
			"\n" +
			"**Warning** This mechanism is provided as an \"escape hatch\" for exceptional situations where a " +
			"first-class Terraform provider is not more appropriate. Its capabilities are limited in comparison " +
			"to a true data source, and implementing a data source via an external program is likely to hurt the " +
			"portability of your Terraform configuration by creating dependencies on external programs and " +
			"libraries that may not be available (or may need to be used differently) on different operating " +
			"systems.\n" +
			"\n" +
			"**Warning** Terraform Enterprise does not guarantee availability of any particular language runtimes " +
			"or external programs beyond standard shell utilities, so it is not recommended to use this data source " +
			"within configurations that are applied within Terraform Enterprise.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"program": schema.ListAttribute{
				Description: "A list of strings, whose first element is the program to run and whose " +
					"subsequent elements are optional command line arguments to the program. Terraform does " +
					"not execute the program through a shell, so it is not necessary to escape shell " +
					"metacharacters nor add quotes around arguments containing spaces.",
				Required:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"working_dir": schema.StringAttribute{
				Description: "Working directory of the program. If not supplied, the program will run " +
					"in the current directory.",
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"query": schema.MapAttribute{
				Description: "A map of string values to pass to the external program as the query " +
					"arguments. If not supplied, the program will receive an empty object as its input.",
				Optional:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"result": schema.MapAttribute{
				Description: "A map of string values to pass to the external program as the query " +
					"arguments. If not supplied, the program will receive an empty object as its input.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *programResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan execModelV0

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	program := make([]string, 0, len(plan.Program.Elements()))

	for _, programArgRaw := range plan.Program.Elements() {
		programArg := strings.Replace(programArgRaw.String(), "\"", "", -1)
		if programArg == "" {
			continue
		}
		program = append(program, programArg)
	}

	if len(program) == 0 {
		resp.Diagnostics.AddError("External Program Missing", "The data source was configured without a program to execute. Verify the configuration contains at least one non-empty value.")
		return
	}

	query := make(map[string]string)

	for key, val := range plan.Query.Elements() {
		valArg := strings.Replace(val.String(), "\"", "", -1)
		if valArg == "" {
			continue
		}
		query[key] = valArg
	}
	queryJson, err := json.Marshal(query)
	if err != nil {
		resp.Diagnostics.AddError("Query Handling Failed", "The data source received an unexpected error while attempting to parse the query. "+
			"This is always a bug in the external provider code and should be reported to the provider developers.")
		return
	}

	// first element is assumed to be an executable command, possibly found
	// using the PATH environment variable.
	_, err = exec.LookPath(program[0])

	if err != nil {
		resp.Diagnostics.AddError("External Program Lookup Failed",
			`The data source received an unexpected error while attempting to find the program.

The program must be accessible according to the platform where Terraform is running.

If the expected program should be automatically found on the platform where Terraform is running, ensure that the program is in an expected directory. On Unix-based platforms, these directories are typically searched based on the '$PATH' environment variable. On Windows-based platforms, these directories are typically searched based on the '%PATH%' environment variable.

If the expected program is relative to the Terraform configuration, it is recommended that the program name includes the interpolated value of 'path.module' before the program name to ensure that it is compatible with varying module usage. For example: "${path.module}/my-program"

The program must also be executable according to the platform where Terraform is running. On Unix-based platforms, the file on the filesystem must have the executable bit set. On Windows-based platforms, no action is typically necessary.
`+
				fmt.Sprintf("\nPlatform: %s", runtime.GOOS)+
				fmt.Sprintf("\nProgram: %s", program[0])+
				fmt.Sprintf("\nError: %s", err))

		return
	}

	cmd := exec.CommandContext(ctx, program[0], program[1:]...)
	cmd.Dir = plan.WorkingDir.ValueString()
	cmd.Stdin = bytes.NewReader(queryJson)

	tflog.Trace(ctx, "Executing external program", map[string]interface{}{"program": cmd.String()})

	resultJson, err := cmd.Output()

	tflog.Trace(ctx, "Executed external program", map[string]interface{}{"program": cmd.String(), "output": string(resultJson)})

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.Stderr != nil && len(exitErr.Stderr) > 0 {
				resp.Diagnostics.AddError("External Program Execution Failed",
					"The data source received an unexpected error while attempting to execute the program."+
						fmt.Sprintf("\n\nProgram: %s", cmd.Path)+
						fmt.Sprintf("\nError Message: %s", string(exitErr.Stderr))+
						fmt.Sprintf("\nState: %s", err))
				return
			}

			resp.Diagnostics.AddError("External Program Execution Failed",
				"The data source received an unexpected error while attempting to execute the program.\n\n"+
					"The program was executed, however it returned no additional error messaging."+
					fmt.Sprintf("\n\nProgram: %s", cmd.Path)+
					fmt.Sprintf("\nState: %s", err))
			return
		}

		resp.Diagnostics.AddError("External Program Execution Failed",
			"The data source received an unexpected error while attempting to execute the program."+
				fmt.Sprintf("\n\nProgram: %s", cmd.Path)+
				fmt.Sprintf("\nError: %s", err))
		return
	}

	result := map[string]interface{}{}
	err = json.Unmarshal(resultJson, &result)
	if err != nil {
		resp.Diagnostics.AddError("Unexpected External Program Results",
			`The data source received unexpected results after executing the program.

Program output must be a JSON encoded map of string keys and string values.

If the error is unclear, the output can be viewed by enabling Terraform's logging at TRACE level. Terraform documentation on logging: https://www.terraform.io/internals/debugging
`+
				fmt.Sprintf("\nProgram: %s", cmd.Path)+
				fmt.Sprintf("\nResult Error: %s", err))
		return
	}

	i := plan
	i.Id = types.StringValue("example-id")

	var d diag.Diagnostics
	i.Result, d = types.MapValueFrom(ctx, types.StringType, result)

	if len(d) > 0 {
		resp.Diagnostics.Append(d...)
	}

	diags = resp.State.Set(ctx, i)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Read does not need to perform any operations as the state in ReadResourceResponse is already populated.
func (r *programResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse) {
}

// Update ensures the plan value is copied to the state to complete the update.
func (r *programResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model execModelV0

	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// Delete does not need to explicitly call resp.State.RemoveResource() as this is automatically handled by the
// [framework](https://github.com/hashicorp/terraform-plugin-framework/pull/301).
func (r *programResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

type execModelV0 struct {
	Id         types.String `tfsdk:"id"`
	Program    types.List   `tfsdk:"program"`
	WorkingDir types.String `tfsdk:"working_dir"`
	Query      types.Map    `tfsdk:"query"`
	Result     types.Map    `tfsdk:"result"`
}
