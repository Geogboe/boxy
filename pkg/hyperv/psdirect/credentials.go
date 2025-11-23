package psdirect

// Credentials represents authentication credentials for PowerShell Direct
type Credentials struct {
	Username string
	Password string
}

// NewCredentials creates new credentials
func NewCredentials(username, password string) Credentials {
	return Credentials{
		Username: username,
		Password: password,
	}
}
