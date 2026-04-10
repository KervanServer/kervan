package build

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func Info() string {
	return "Kervan " + Version + " (" + Commit + ") built " + Date
}
