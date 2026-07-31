// Package config owns ~/.config/smartmeter-fetch/credentials.enc, a single
// passphrase-encrypted file holding every profile's name/provider/username/
// password.
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

// Profile is one stored portal login.
type Profile struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// credentials.enc layout: [1 byte version][16 salt][12 nonce][ciphertext].
// Key = argon2id(passphrase, salt, t=1, m=64MiB, p=4, len=32); AES-256-GCM.
// Ciphertext, once decrypted, is a JSON-encoded []Profile.
const (
	blobVersion  = 1
	saltLen      = 16
	nonceLen     = 12
	keyLen       = 32
	argonTime    = 1
	argonMemKiB  = 64 * 1024
	argonThreads = 4
)

// Dir returns the config directory (not created yet). Honors
// SMARTMETER_CONFIG_DIR when set, so credentials.enc can live somewhere
// other than the OS default (e.g. an encrypted volume).
func Dir() (string, error) {
	if d := os.Getenv("SMARTMETER_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "smartmeter-fetch"), nil
}

func credPath(dir string) string { return filepath.Join(dir, "credentials.enc") }

// CredentialsExist reports whether a credentials.enc file exists in dir.
func CredentialsExist(dir string) bool {
	_, err := os.Stat(credPath(dir))
	return err == nil
}

// LoadSecrets returns every profile (name, username, password) stored in
// dir, decrypted with passphrase. A missing credentials.enc returns an
// empty slice, no error, no decryption attempted.
func LoadSecrets(dir string, passphrase []byte) ([]Profile, error) {
	blob, err := os.ReadFile(credPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pt, err := decrypt(passphrase, blob)
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	if err := json.Unmarshal(pt, &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// SaveSecrets encrypts and writes every profile in profiles to dir's
// credentials.enc.
func SaveSecrets(dir string, passphrase []byte, profiles []Profile) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	pt, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	blob, err := encrypt(passphrase, pt)
	if err != nil {
		return err
	}
	// Write to a temp file and rename over the real path so a crash or
	// full disk mid-write can't leave a truncated/corrupt credentials.enc
	// (os.Rename is atomic on POSIX, replace-on-existing on Windows).
	tmp := credPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, credPath(dir))
}

func deriveKey(passphrase, salt []byte) []byte {
	return argon2.IDKey(passphrase, salt, argonTime, argonMemKiB, argonThreads, keyLen)
}

func encrypt(passphrase, plaintext []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	out := append([]byte{blobVersion}, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, nil), nil
}

func decrypt(passphrase, blob []byte) ([]byte, error) {
	if len(blob) < 1+saltLen+nonceLen {
		return nil, errors.New("credentials file corrupt or unsupported version")
	}
	if blob[0] != blobVersion {
		return nil, fmt.Errorf("credentials file has unsupported version %d", blob[0])
	}
	salt := blob[1 : 1+saltLen]
	nonce := blob[1+saltLen : 1+saltLen+nonceLen]
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, blob[1+saltLen+nonceLen:], nil)
	if err != nil {
		return nil, errors.New("wrong passphrase or corrupt credentials file")
	}
	return pt, nil
}
