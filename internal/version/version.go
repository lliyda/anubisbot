package version

// Set via -ldflags "-X anubisbot/internal/version.Version=1.0.0"
var (
	Version = "dev"
	Commit  = ""
	BuiltAt = ""
)

func String() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
