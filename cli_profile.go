package main

import (
	"bufio"
	"bytes"
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

func profileAdd(dir, name, username, password string) error {
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
	secrets = append(secrets, config.Profile{Name: name, Username: username, Password: password})
	return config.SaveSecrets(dir, pass, secrets)
}

// applyProfileFields mutates secrets[idx], applying username/password where
// non-empty (each "" means "leave unchanged").
func applyProfileFields(secrets []config.Profile, idx int, username, password string) {
	if username != "" {
		secrets[idx].Username = username
	}
	if password != "" {
		secrets[idx].Password = password
	}
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
	fmt.Fprint(w, `Manage stored portal credentials (credentials.enc).

Usage:
  smartmeter-fetch profile add <name>       add a profile (prompts for username/password)
  smartmeter-fetch profile list             list configured profile names/usernames
  smartmeter-fetch profile update <name>    change a profile's username/password (blank keeps current)
  smartmeter-fetch profile remove <name>    remove a profile
  smartmeter-fetch profile passphrase       change the master passphrase (re-encrypts everything)
`)
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
		if len(args) != 2 {
			printProfileUsage(stderr)
			return 2
		}
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
		if err := profileAdd(dir, name, username, password); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "profile", name, "added")
		return 0
	case "update":
		if len(args) != 2 {
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
		applyProfileFields(secrets, idx, username, password)
		if err := config.SaveSecrets(dir, pass, secrets); err != nil {
			fmt.Fprintln(stderr, "smartmeter-fetch:", err)
			return 1
		}
		fmt.Fprintln(stdout, "profile", name, "updated")
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
			fmt.Fprintf(stdout, "%s\t%s\n", p.Name, p.Username)
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
