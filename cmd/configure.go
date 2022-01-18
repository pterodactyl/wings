package cmd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/spf13/cobra"

	"github.com/pterodactyl/wings/config"
)

var configureArgs struct {
	PanelURL      string
	Token         string
	ConfigPath    string
	Node          string
	Override      bool
	AllowInsecure bool
}

var nodeIdRegex = regexp.MustCompile(`^(\d+)$`)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Use a token to configure wings automatically",
	Run:   configureCmdRun,
}

func init() {
	configureCmd.PersistentFlags().StringVarP(&configureArgs.PanelURL, "panel-url", "p", "", "The base URL for this daemon's panel")
	configureCmd.PersistentFlags().StringVarP(&configureArgs.Token, "token", "t", "", "The API key to use for fetching node information")
	configureCmd.PersistentFlags().StringVarP(&configureArgs.Node, "node", "n", "", "The ID of the node which will be connected to this daemon")
	configureCmd.PersistentFlags().StringVarP(&configureArgs.ConfigPath, "config-path", "c", config.DefaultLocation, "The path where the configuration file should be made")
	configureCmd.PersistentFlags().BoolVar(&configureArgs.Override, "override", false, "Set to true to override an existing configuration for this node")
	configureCmd.PersistentFlags().BoolVar(&configureArgs.AllowInsecure, "allow-insecure", false, "Set to true to disable certificate checking")
}

func configureCmdRun(cmd *cobra.Command, args []string) {
	if configureArgs.AllowInsecure {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	if _, err := os.Stat(configureArgs.ConfigPath); err == nil && !configureArgs.Override {
		survey.AskOne(&survey.Confirm{Message: "Override existing configuration file"}, &configureArgs.Override)
		if !configureArgs.Override {
			fmt.Println("Aborting process; a configuration file already exists for this node.")
			os.Exit(1)
		}
	} else if err != nil && !os.IsNotExist(err) {
		panic(err)
	}

	var questions []*survey.Question
	if configureArgs.PanelURL == "" {
		questions = append(questions, &survey.Question{
			Name:   "PanelURL",
			Prompt: &survey.Input{Message: "Panel URL: "},
			Validate: func(ans interface{}) error {
				if str, ok := ans.(string); ok {
					_, err := url.ParseRequestURI(str)
					return err
				}
				return nil
			},
		})
	}

	if configureArgs.Token == "" {
		questions = append(questions, &survey.Question{
			Name:   "Token",
			Prompt: &survey.Input{Message: "API Token: "},
			Validate: func(ans interface{}) error {
				if str, ok := ans.(string); ok {
					if len(str) == 0 {
						return fmt.Errorf("please provide a valid authentication token")
					}
				}
				return nil
			},
		})
	}

	if configureArgs.Node == "" {
		questions = append(questions, &survey.Question{
			Name:   "Node",
			Prompt: &survey.Input{Message: "Node ID: "},
			Validate: func(ans interface{}) error {
				if str, ok := ans.(string); ok {
					if !nodeIdRegex.Match([]byte(str)) {
						return fmt.Errorf("please provide a valid authentication token")
					}
				}
				return nil
			},
		})
	}

	if err := survey.Ask(questions, &configureArgs); err != nil {
		if err == terminal.InterruptErr {
			return
		}

		panic(err)
	}

	c := &http.Client{
		Timeout: time.Second * 30,
	}

	req, err := getRequest()
	if err != nil {
		panic(err)
	}

	fmt.Printf("%+v", req.Header)
	fmt.Printf(req.URL.String())

	res, err := c.Do(req)
	if err != nil {
		fmt.Println("Failed to fetch configuration from the panel.\n", err.Error())
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized {
		fmt.Println("The authentication credentials provided were not valid.")
		os.Exit(1)
	} else if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)

		fmt.Println("An error occurred while processing this request.\n", string(b))
		os.Exit(1)
	}

	b, err := io.ReadAll(res.Body)

	cfg, err := config.NewAtPath(configPath)
	if err != nil {
		panic(err)
	}

	if err := json.Unmarshal(b, cfg); err != nil {
		panic(err)
	}

	if err = config.WriteToDisk(cfg); err != nil {
		panic(err)
	}

	fmt.Println("Successfully configured wings.")
}

func getRequest() (*http.Request, error) {
	u, err := url.Parse(configureArgs.PanelURL)
	if err != nil {
		panic(err)
	}

	u.Path = path.Join(u.Path, fmt.Sprintf("api/application/nodes/%s/configuration", configureArgs.Node))

	r, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	r.Header.Set("Accept", "application/vnd.pterodactyl.v1+json")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", configureArgs.Token))

	return r, nil
}
