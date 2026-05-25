package platform

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// InitLogger configures zerolog. When LOG_FORMAT=pretty (or LOG_PRETTY=true),
// uses colorized console output for development. Otherwise outputs JSON for
// production/log aggregation.
func InitLogger() {
	zerolog.TimeFieldFormat = time.RFC3339

	if os.Getenv("LOG_FORMAT") == "pretty" || os.Getenv("LOG_PRETTY") == "true" {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: "15:04:05",
		}).With().Timestamp().Logger()
	} else {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
}
