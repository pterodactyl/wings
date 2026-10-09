package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	dockersystem "github.com/docker/docker/api/types/system"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/docker/docker/pkg/parsers/operatingsystem"
	"github.com/spf13/cobra"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/pterodactyl/wings/system"
)

const (
	DefaultHastebinUrl = "https://ptero.co"
	DefaultLogLines    = 200
)

// hastebinTimeout is how long uploading the report may take.
var hastebinTimeout = 30 * time.Second

var diagnosticsArgs struct {
	IncludeEndpoints   bool
	IncludeLogs        bool
	ReviewBeforeUpload bool
	HastebinURL        string
	LogLines           int
}

func newDiagnosticsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "diagnostics",
		Short: "Collect and report information about this Wings instance to assist in debugging.",
		PreRun: func(cmd *cobra.Command, args []string) {
			initConfig()
			log.SetHandler(cli.Default)
		},
		Run: diagnosticsCmdRun,
	}

	command.Flags().StringVar(&diagnosticsArgs.HastebinURL, "hastebin-url", DefaultHastebinUrl, "the url of the hastebin instance to use")
	command.Flags().IntVar(&diagnosticsArgs.LogLines, "log-lines", DefaultLogLines, "the number of log lines to include in the report")

	return command
}

// diagnosticsCmdRun collects diagnostics about wings, its configuration and the node.
// We collect:
// - wings and docker versions
// - relevant parts of daemon configuration
// - the docker debug output
// - running docker containers
// - logs
func diagnosticsCmdRun(*cobra.Command, []string) {
	questions := []*survey.Question{
		{
			Name:   "IncludeEndpoints",
			Prompt: &survey.Confirm{Message: "Do you want to include endpoints (i.e. the FQDN/IP of your panel)?", Default: false},
		},
		{
			Name:   "IncludeLogs",
			Prompt: &survey.Confirm{Message: "Do you want to include the latest logs?", Default: true},
		},
		{
			Name: "ReviewBeforeUpload",
			Prompt: &survey.Confirm{
				Message: "Do you want to review the collected data before uploading to " + diagnosticsArgs.HastebinURL + "?",
				Help:    "The data, especially the logs, might contain sensitive information, so you should review it. You will be asked before anything is uploaded.",
				Default: true,
			},
		},
	}
	if err := survey.Ask(questions, &diagnosticsArgs); err != nil {
		if err == terminal.InterruptErr {
			return
		}
		panic(err)
	}

	dockerVersion, dockerInfo, dockerErr := getDockerInfo()

	output := &strings.Builder{}
	fmt.Fprintln(output, "Pterodactyl Wings - Diagnostics Report")
	printHeader(output, "Versions")
	fmt.Fprintln(output, "               Wings:", system.Version)
	if dockerErr == nil {
		fmt.Fprintln(output, "              Docker:", dockerVersion.Version)
	}
	if v, err := kernel.GetKernelVersion(); err == nil {
		fmt.Fprintln(output, "              Kernel:", v)
	}
	if os, err := operatingsystem.GetOperatingSystem(); err == nil {
		fmt.Fprintln(output, "                  OS:", os)
	}

	printHeader(output, "Wings Configuration")
	cfg := writeConfiguration(output)

	printHeader(output, "Docker: Info")
	if dockerErr == nil {
		fmt.Fprintln(output, "Server Version:", dockerInfo.ServerVersion)
		fmt.Fprintln(output, "Storage Driver:", dockerInfo.Driver)
		if dockerInfo.DriverStatus != nil {
			for _, pair := range dockerInfo.DriverStatus {
				fmt.Fprintf(output, "  %s: %s\n", pair[0], pair[1])
			}
		}
		if dockerInfo.SystemStatus != nil {
			for _, pair := range dockerInfo.SystemStatus {
				fmt.Fprintf(output, " %s: %s\n", pair[0], pair[1])
			}
		}
		fmt.Fprintln(output, "LoggingDriver:", dockerInfo.LoggingDriver)
		fmt.Fprintln(output, " CgroupDriver:", dockerInfo.CgroupDriver)
		if len(dockerInfo.Warnings) > 0 {
			for _, w := range dockerInfo.Warnings {
				fmt.Fprintln(output, w)
			}
		}
	} else {
		fmt.Fprintln(output, dockerErr.Error())
	}

	printHeader(output, "Docker: Running Containers")
	c := exec.Command("docker", "ps")
	if co, err := c.Output(); err == nil {
		output.Write(co)
	} else {
		fmt.Fprint(output, "Couldn't list containers: ", err)
	}

	printHeader(output, "Latest Wings Logs")
	if diagnosticsArgs.IncludeLogs {
		p := "/var/log/pterodactyl/wings.log"
		if cfg != nil {
			p = path.Join(cfg.System.LogDirectory, "wings.log")
		}
		if c, err := exec.Command("tail", "-n", strconv.Itoa(diagnosticsArgs.LogLines), p).Output(); err != nil { //nolint:gosec // fixed binary, no shell; path comes from the operator's config
			fmt.Fprintln(output, "No logs found or an error occurred.")
		} else {
			fmt.Fprintf(output, "%s\n", string(c))
		}
	} else {
		fmt.Fprintln(output, "Logs redacted.")
	}

	if !diagnosticsArgs.IncludeEndpoints && cfg != nil {
		s := output.String()
		output.Reset()
		for _, v := range []string{cfg.PanelLocation, cfg.Api.Host, cfg.Api.Ssl.CertificateFile, cfg.Api.Ssl.KeyFile, cfg.System.Sftp.Address} {
			// Replacing an empty string would insert the placeholder between every character.
			if v != "" {
				s = strings.ReplaceAll(s, v, "{redacted}")
			}
		}
		output.WriteString(s)
	}

	if diagnosticsArgs.ReviewBeforeUpload {
		fmt.Println("\n---------------  generated report  ---------------")
		fmt.Println(output.String())
		fmt.Print("---------------   end of report    ---------------\n\n")
	}

	// Always ask before uploading, since the report can contain sensitive
	// information. A failed or interrupted prompt leaves upload false.
	var upload bool
	_ = survey.AskOne(&survey.Confirm{Message: "Upload to " + diagnosticsArgs.HastebinURL + "?", Default: false}, &upload)
	if upload {
		u, err := uploadToHastebin(diagnosticsArgs.HastebinURL, output.String())
		if err == nil {
			fmt.Println("Your report is available here: ", u)
		}
	}
}

// writeConfiguration writes the relevant parts of the configuration file Wings
// was started with, and returns it if it could be read.
func writeConfiguration(w io.Writer) *config.Configuration {
	var cfg *config.Configuration
	if err := config.FromFile(configPath); err != nil {
		fmt.Fprintln(w, "Failed to load configuration:", err)
	} else {
		cfg = config.Get()
		fmt.Fprintln(w, "      Panel Location:", redact(cfg.PanelLocation))
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  Internal Webserver:", redact(cfg.Api.Host), ":", cfg.Api.Port)
		fmt.Fprintln(w, "         SSL Enabled:", cfg.Api.Ssl.Enabled)
		fmt.Fprintln(w, "     SSL Certificate:", redact(cfg.Api.Ssl.CertificateFile))
		fmt.Fprintln(w, "             SSL Key:", redact(cfg.Api.Ssl.KeyFile))
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "         SFTP Server:", redact(cfg.System.Sftp.Address), ":", cfg.System.Sftp.Port)
		fmt.Fprintln(w, "      SFTP Read-Only:", cfg.System.Sftp.ReadOnly)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "      Root Directory:", cfg.System.RootDirectory)
		fmt.Fprintln(w, "      Logs Directory:", cfg.System.LogDirectory)
		fmt.Fprintln(w, "      Data Directory:", cfg.System.Data)
		fmt.Fprintln(w, "   Archive Directory:", cfg.System.ArchiveDirectory)
		fmt.Fprintln(w, "    Backup Directory:", cfg.System.BackupDirectory)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "            Username:", cfg.System.Username)
		fmt.Fprintln(w, "         Server Time:", time.Now().Format(time.RFC1123Z))
		fmt.Fprintln(w, "          Debug Mode:", cfg.Debug)
	}
	return cfg
}

func getDockerInfo() (types.Version, dockersystem.Info, error) {
	client, err := environment.Docker()
	if err != nil {
		return types.Version{}, dockersystem.Info{}, err
	}
	dockerVersion, err := client.ServerVersion(context.Background())
	if err != nil {
		return types.Version{}, dockersystem.Info{}, err
	}
	dockerInfo, err := client.Info(context.Background())
	if err != nil {
		return types.Version{}, dockersystem.Info{}, err
	}
	return dockerVersion, dockerInfo, nil
}

func uploadToHastebin(hbUrl, content string) (string, error) {
	r := strings.NewReader(content)
	u, err := url.Parse(hbUrl)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, "documents")
	client := &http.Client{Timeout: hastebinTimeout}
	res, err := client.Post(u.String(), "text/plain", r)
	if err != nil {
		fmt.Println("Failed to upload report to ", u.String(), err)
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fmt.Println("Failed to upload report to ", u.String(), res.Status)
		return "", errors.New("unexpected response status: " + res.Status)
	}
	pres := make(map[string]interface{})
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		fmt.Println("Failed to parse response.", err)
		return "", err
	}
	if err := json.Unmarshal(body, &pres); err != nil {
		fmt.Println("Failed to parse response.", err)
		return "", err
	}
	if key, ok := pres["key"].(string); ok {
		u, _ := url.Parse(hbUrl)
		u.Path = path.Join(u.Path, key)
		return u.String(), nil
	}
	return "", errors.New("failed to find key in response")
}

func redact(s string) string {
	if !diagnosticsArgs.IncludeEndpoints {
		return "{redacted}"
	}
	return s
}

func printHeader(w io.Writer, title string) {
	fmt.Fprintln(w, "\n|\n|", title)
	fmt.Fprintln(w, "| ------------------------------")
}
