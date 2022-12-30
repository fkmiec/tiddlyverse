package main

import (
	//"encoding/csv"
	"flag"
	"fmt"
	"strings"

	"github.com/fkmiec/tiddlyverse"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	authTokenAnon = "(anon)"
	//authTokenAuthenticated = "(authenticated)"
)

func main() {
	flag.String("host", "localhost", "the hostname or IP for the server URL (need to specify to support custom paths for multiple wikis)")
	flag.String("port", "8080", "the port to serve this page on")
	flag.String("debug_level", "info", "specify the debug level. options are: trace, debug, info, warn, error, fatal")
	flag.String("credentials_file", "", "the name of the credentials CSV in the root wiki directory")
	flag.String("readers", authTokenAnon, "specify the security principals with read access to the wiki")
	flag.String("writers", authTokenAnon, "specify the security principals with write access to the wiki")

	viper.BindEnv("host")
	viper.BindEnv("port")
	viper.BindEnv("wiki_location")
	viper.BindEnv("debug_level")

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	viper.BindPFlags(pflag.CommandLine)

	if pflag.NArg() > 0 {
		viper.Set("wiki_location", pflag.Arg(0))
	}
	if !viper.IsSet("wiki_location") {
		panic("wiki location must be specified as an environment variable or as the last argument to this call")
	}

	var err error

	// setup the logger
	logLevel, err := zerolog.ParseLevel(viper.GetString("debug_level"))
	if err != nil {
		panic(fmt.Sprintf("debug_level '%s' not recognized: %v", viper.GetString("debug_level"), err))
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Logger.Level(logLevel).With().Logger()
	if log.Logger.GetLevel() < zerolog.InfoLevel {
		log.Logger = log.Logger.With().Caller().Logger()
	}

	// created a TiddlerStore
	locationSplit := strings.Split(viper.GetString("wiki_location"), "://")
	storageType := locationSplit[0]
	storageLocation := locationSplit[1]

	log.Fatal().Err(tiddlybucket.ListenAndServe(fmt.Sprintf("%s:%s", viper.GetString("host"), viper.GetString("port")), viper.GetString("credentials_file"), viper.GetString("readers"), viper.GetString("writers"), storageType, storageLocation)).
		Msg("server shutdown with error")
}
