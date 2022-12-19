package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func New() provider.Provider {
	return &p{}
}

var _ provider.Provider = (*p)(nil)

type p struct{}

func (p *p) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "external"
}

func (p *p) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {
}

func (p *p) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *p) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewExternalResource,
	}
}

func (p *p) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
