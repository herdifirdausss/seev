package logger

// Config configures the application's structured logger.
type Config struct {
	Level   string
	Format  string
	AppName string
	Env     string
}
