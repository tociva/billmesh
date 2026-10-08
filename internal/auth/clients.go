package auth

// ClientType is the fixed Billmesh API surface assigned to an OAuth client.
// It is derived from trusted server configuration, never from token input.
type ClientType string

const (
	ClientCatalogue ClientType = "catalogue"
	ClientBilling   ClientType = "billing"
	ClientRuntime   ClientType = "runtime"
	ClientAdmin     ClientType = "admin"
	ClientConsole   ClientType = "console"
)

func (c *Claims) IsClient(types ...ClientType) bool {
	for _, clientType := range types {
		if c.ClientType == clientType {
			return true
		}
	}
	return false
}

func (c *Claims) CanLinkProducts() bool {
	return c.IsClient(ClientAdmin)
}
