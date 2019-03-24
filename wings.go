package main

import (
    "flag"
    "fmt"
    "go.uber.org/zap"
)

var (
    configPath string
    debug      bool
)

// Entrypoint for the Wings application. Configures the logger and checks any
// flags that were passed through in the boot arguments.
func main() {
    flag.StringVar(&configPath, "config", "config.yml", "set the location for the configuration file")
    flag.BoolVar(&debug, "debug", false, "pass in order to run wings in debug mode")

    flag.Parse()

    printLogo()
    if err := configureLogging(); err != nil {
        panic(err)
    }

    if debug {
        zap.S().Debugw("running in debug mode")
    }

    zap.S().Infof("using configuration file: %s", configPath)
}

// Configures the global logger for Zap so that we can call it from any location
// in the code without having to pass around a logger instance.
func configureLogging() error {
    cfg := zap.NewProductionConfig()
    if debug {
        cfg = zap.NewDevelopmentConfig()
    }

    cfg.Encoding = "console"
    cfg.OutputPaths = []string{
        "stdout",
    }

    logger, err := cfg.Build()
    if err != nil {
        return err
    }

    zap.ReplaceGlobals(logger)

    return nil
}

// Prints the wings logo, nothing special here!
func printLogo() {
    fmt.Println()
    fmt.Println(`                     ____`)
    fmt.Println(`__ Pterodactyl _____/___/_______ _______ ______`)
    fmt.Println(`\_____\    \/\/    /   /       /  __   /   ___/`)
    fmt.Println(`   \___\          /   /   /   /  /_/  /___   /`)
    fmt.Println(`        \___/\___/___/___/___/___    /______/`)
    fmt.Println(`                            /_______/ v` + Version)
    fmt.Println()
}
