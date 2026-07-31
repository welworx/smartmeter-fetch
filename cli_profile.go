package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/welworx/smartmeter-fetch/internal/config"
)

// readPassphrase returns the credentials.enc master passphrase:
// SMARTMETER_PASSPHRASE if set (cron/scripting), else an interactive
// prompt. confirmNew prompts twice — use when the file doesn't exist yet.
func readPassphrase(confirmNew bool) ([]byte, error) {
	if p := os.Getenv("SMARTMETER_PASSPHRASE"); p != "" {
		return []byte(p), nil
	}
	p1, err := promptSecret("Passphrase: ")
	if err != nil {
		return nil, err
	}
	if confirmNew {
		p2, err := promptSecret("Repeat passphrase: ")
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(p1, p2) {
			return nil, errors.New("passphrases do not match")
		}
	}
	return p1, nil
}

func promptSecret(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	p, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return p, err
}

func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func profileAdd(dir, name, providerName, username, password string) error {
	pass, err := readPassphrase(!config.CredentialsExist(dir))
	if err != nil {
		return err
	}
	secrets, err := config.LoadSecrets(dir, pass)
	if err != nil {
		return err
	}
	for _, p := range secrets {
		if p.Name == name {
			return fmt.Errorf("profile %q already exists", name)
		}
	}
	secrets = append(secrets, config.Profile{Name: name, Provider: providerName, Username: username, Password: password})
	return config.SaveSecrets(dir, pass, secrets)
}

// applyProfileFields mutates secrets[idx], applying provider/username/
// password where non-empty (each "" means "leave unchanged").
func applyProfileFields(secrets []config.Profile, idx int, providerName, username, password string) {
	if providerName != "" {
		secrets[idx].Provider = providerName
	}
	if username != "" {
		secrets[idx].Username = username
	}
	if password != "" {
		secrets[idx].Password = password
	}
}

// testLogin verifies username/password against providerName's portal by
// actually logging in (ListPoints forces a login internally). Used by
// "profile add"/"profile update" (so a typo'd password is never silently
// stored) and "profile verify" (to recheck credentials already stored).
func testLogin(ctx context.Context, providerName, username, password string) error {
	factory, ok := providerFactories[providerName]
	if !ok {
		return fmt.Errorf("unknown provider %q (only \"evn\" is supported)", providerName)
	}
	if _, err := factory(username, password, "", nil).ListPoints(ctx); err != nil {
		return fmt.Errorf("login to %s failed: %w", providerName, err)
	}
	return nil
}

// parseNameProviderArgs validates the arg shape shared by `add`/`update`:
// [name] or [name, "-provider", value]. Returns the provider override ("" if
// none given) and whether args matched one of those two shapes.
func parseNameProviderArgs(args []string) (providerName string, ok bool) {
	switch len(args) {
	case 2:
		return "", true
	case 4:
		if args[2] == "-provider" {
			return args[3], true
		}
	}
	return "", false
}

func profileRemove(dir, name string) error {
	if !config.CredentialsExist(dir) {
		return fmt.Errorf("no profile %q", name)
	}
	pass, err := readPassphrase(false)
	if err != nil {
		return err
	}
	secrets, err := config.LoadSecrets(dir, pass)
	if err != nil {
		return err
	}
	kept := secrets[:0]
	found := false
	for _, p := range secrets {
		if p.Name == name {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return fmt.Errorf("no profile %q", name)
	}
	return config.SaveSecrets(dir, pass, kept)
}

// profileChangePassphrase re-encrypts every profile under a new
// passphrase, prompted twice to confirm. Always interactive — never read
// from SMARTMETER_PASSPHRASE, which already means "current passphrase"
// everywhere else in the CLI. No profile data is changed.
func profileChangePassphrase(dir string) error {
	if !config.CredentialsExist(dir) {
		return errors.New("no credentials.enc to rekey")
	}
	oldPass, err := readPassphrase(false)
	if err != nil {
		return err
	}
	secrets, err := config.LoadSecrets(dir, oldPass)
	if err != nil {
		return err
	}
	newPass, err := promptSecret("New passphrase: ")
	if err != nil {
		return err
	}
	confirm, err := promptSecret("Repeat new passphrase: ")
	if err != nil {
		return err
	}
	if !bytes.Equal(newPass, confirm) {
		return errors.New("passphrases do not match")
	}
	return config.SaveSecrets(dir, newPass, secrets)
}

func printProfileUsage(w io.Writer) {
	fmt.Fprint(w, `Manage stored portal credentials (credentials.enc). "add"/"update" verify
the username/password by logging into the portal before saving.

Usage:
  smartmeter-fetch profile add <name> [-provider evn]      add a profile (prompts for username/password)
  smartmeter-fetch profile list                            list configured profiles (name, provider, username)
  smartmeter-fetch profile update <name> [-provider evn]   change a profile's provider/username/password (blank keeps current)
  smartmeter-fetch profile remove <name>                   remove a profile
  smartmeter-fetch profile verify [name]                   log into the portal with each stored profile (or just
                                                             one, by name) to check its credentials are still valid
  smartmeter-fetch profile passphrase                      change the master passphrase (re-encrypts everything)
`)
}

// profileVerify logs into each profile's portal with its stored credentials
// and reports OK/FAILED to stdout, one line per profile. Returns the number
// that failed, so callers can pick an exit code without re-deriving it.
func profileVerify(ctx context.Context, stdout io.Writer, profiles []config.Profile) (failed int) {
	for _, p := range profiles {
		if err := testLogin(ctx, providerOrDefault(p.Provider), p.Username, p.Password); err != nil {
			fmt.Fprintf(stdout, "%s\tFAILED\t%v\n", p.Name, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "%s\tOK\n", p.Name)
	}
	return failed
}

// runProfile handles `smartmeter-fetch profile <add|list|update|remove|passphrase> ...`.
func runProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printProfileUsage(stderr)
		return 2
	}
	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(stderr, "smartmeter-fetch:", err)
		return 1
	}
	switch args[0] {
	case "add":
		providerName, ok := parseNameProviderArgs(args)
		if !ok {
			printProfileUsage(stderr)
			return 2
		}
		providerName = providerOrDefault(providerName)
		name := args[1]
		username := os.Getenv("SMARTMETER_USER")
		if username == "" {
			username, err = promptLine("Username: ")
			if err != nil {
				fmt.Fprintln(stderr, "smartmeter-fetch:", err)
				return 1
			}
		}
		password := os.Getenv("SMARTMETER_PASSWORD")
		if password == "" {
			pw, err := promptSecret("Portal password: ")
			if err != nil {
				fmt.Fprintln(stderr, "smartmeter-fetch:", err)
				return 1
			}
			password = string(pw)
		}
		if err := testLogin(context.Background(), providerName, username, password); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		if err := profileAdd(dir, name, providerName, username, password); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "profile", name, "added (login verified)")
		return 0
	case "update":
		providerName, ok := parseNameProviderArgs(args)
		if !ok {
			printProfileUsage(stderr)
			return 2
		}
		name := args[1]
		if !config.CredentialsExist(dir) {
			fmt.Fprintf(stderr, "smartmeter-fetch: no profile %q\n", name)
			return 1
		}
		pass, err := readPassphrase(false)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		secrets, err := config.LoadSecrets(dir, pass)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		idx := -1
		for i, p := range secrets {
			if p.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			fmt.Fprintf(stderr, "smartmeter-fetch: no profile %q\n", name)
			return 1
		}
		username := os.Getenv("SMARTMETER_USER")
		if username == "" {
			username, err = promptLine(fmt.Sprintf("New username [%s] (leave blank to keep): ", secrets[idx].Username))
			if err != nil {
				fmt.Fprintln(stderr, "smartmeter-fetch:", err)
				return 1
			}
		}
		password := os.Getenv("SMARTMETER_PASSWORD")
		if password == "" {
			pw, err := promptSecret("New portal password (leave blank to keep current): ")
			if err != nil {
				fmt.Fprintln(stderr, "smartmeter-fetch:", err)
				return 1
			}
			password = string(pw)
		}
		applyProfileFields(secrets, idx, providerName, username, password)
		if err := testLogin(context.Background(), providerOrDefault(secrets[idx].Provider), secrets[idx].Username, secrets[idx].Password); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		if err := config.SaveSecrets(dir, pass, secrets); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "profile", name, "updated (login verified)")
		return 0
	case "list":
		if len(args) != 1 {
			printProfileUsage(stderr)
			return 2
		}
		if !config.CredentialsExist(dir) {
			return 0
		}
		pass, err := readPassphrase(false)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		secrets, err := config.LoadSecrets(dir, pass)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		for _, p := range secrets {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.Name, providerOrDefault(p.Provider), p.Username)
		}
		return 0
	case "verify":
		if len(args) > 2 {
			printProfileUsage(stderr)
			return 2
		}
		if !config.CredentialsExist(dir) {
			return 0
		}
		pass, err := readPassphrase(false)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		secrets, err := config.LoadSecrets(dir, pass)
		if err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		if len(args) == 2 {
			name := args[1]
			idx := -1
			for i, p := range secrets {
				if p.Name == name {
					idx = i
					break
				}
			}
			if idx == -1 {
				fmt.Fprintf(stderr, "smartmeter-fetch: no profile %q\n", name)
				return 1
			}
			secrets = secrets[idx : idx+1]
		}
		if profileVerify(context.Background(), stdout, secrets) > 0 {
			return 1
		}
		return 0
	case "remove":
		if len(args) != 2 {
			printProfileUsage(stderr)
			return 2
		}
		if err := profileRemove(dir, args[1]); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "profile", args[1], "removed")
		return 0
	case "passphrase":
		if len(args) != 1 {
			printProfileUsage(stderr)
			return 2
		}
		if err := profileChangePassphrase(dir); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "passphrase changed")
		return 0
	default:
		printProfileUsage(stderr)
		return 2
	}
}
