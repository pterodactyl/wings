package cmd

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/pterodactyl/wings/config"
	"github.com/spf13/cobra"
)

var (
	configureArgs struct {
		PanelURL string
		Token    string
		Override bool
	}
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Use a token to configure wings automatically",

	Run: configureCmdRun,
}

func init() {
	configureCmd.PersistentFlags().StringVarP(&configureArgs.PanelURL, "panel-url", "p", "", "the baseurl of the pterodactyl panel to fetch the configuration from")
	configureCmd.PersistentFlags().StringVarP(&configureArgs.Token, "token", "t", "", "the auto-deploy token to use")
	configureCmd.PersistentFlags().BoolVar(&configureArgs.Override, "override", false, "override an existing configuration")
}

func configureCmdRun(cmd *cobra.Command, args []string) {
	_, err := os.Stat("config.yml")
	if err != os.ErrNotExist && !configureArgs.Override {
		survey.AskOne(&survey.Confirm{Message: "Override existing configuration file"}, &configureArgs.Override)
		if !configureArgs.Override {
			fmt.Println("Aborted.")
			os.Exit(1)
		}
	}

	questions := []*survey.Question{}
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
			Prompt: &survey.Input{Message: "Token: "},
			Validate: func(ans interface{}) error {
				if str, ok := ans.(string); ok {
					if len(str) != 32 {
						return fmt.Errorf("the token needs to have 32 characters")
					}
				}
				return nil
			},
		})
	}

	err = survey.Ask(questions, &configureArgs)
	if err == terminal.InterruptErr {
		return
	}
	if err != nil {
		panic(err)
	}

	url, err := url.Parse(configureArgs.PanelURL)
	url.Path = path.Join(url.Path, "daemon/configure", configureArgs.Token)
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		panic(err)
	}
	req.Header.Add("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("Couldn't fetch configuration from panel.\n", err.Error())
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		fmt.Println("The provided token is invalid.")
		os.Exit(1)
	}
	configJSON, err := ioutil.ReadAll(res.Body)

	cfg := config.Configuration{}
	json.Unmarshal(configJSON, &cfg)
	err = cfg.WriteToDisk()
	if err != nil {
		panic(err)
	}

	fmt.Println("Successfully configured wings.")
}
