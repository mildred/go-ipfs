package config

import (
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"os"

	u "github.com/jbenet/go-ipfs/util"
)

// Identity tracks the configuration of the local node's identity.
type Identity struct {
	PeerID  string
	PrivKey string
	Address string
}

// Datastore tracks the configuration of the datastore.
type Datastore struct {
	Type string
	Path string
}

type SavedPeer struct {
	Address string
	PeerID  string // until multiaddr supports ipfs, use another field.
}

// Config is used to load IPFS config files.
type Config struct {
	Identity   *Identity    // local node's peer identity
	Datastore  Datastore    // local node's storage
	RPCAddress string       // local node's RPC address
	Peers      []*SavedPeer // local nodes's bootstrap peers
}

// Return the default configuration root directory
func GetDefaultPathRoot() (string, error) {
	dir := os.Getenv("IPFS_CONFIG_DIR")
	var err error
	if len(dir) == 0 {
		dir, err = u.TildeExpansion("~/.go-ipfs")
	}
	return dir, err
}

// Return the configuration file path given a configuration root directory
// If the configuration root directory is empty, use the default one
func GetConfigFilePath(configroot string) (string, error) {
	if len(configroot) == 0 {
		dir, err := GetDefaultPathRoot()
		return dir + "/config", err
	} else {
		return configroot + "/config", nil
	}
}

func (i *Identity) DecodePrivateKey(passphrase string) (crypto.PrivateKey, error) {
	pkb, err := base64.StdEncoding.DecodeString(i.PrivKey)
	if err != nil {
		return nil, err
	}

	// currently storing key unencrypted. in the future we need to encrypt it.
	// TODO(security)
	return x509.ParsePKCS1PrivateKey(pkb)
}

// Load reads given file and returns the read config, or error.
func Load(filename string) (*Config, error) {
	// if nothing is there, fail. User must run 'ipfs init'
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, errors.New("ipfs not initialized, please run 'ipfs init'")
	}

	var cfg Config
	err := ReadConfigFile(filename, &cfg)
	if err != nil {
		return nil, err
	}

	// tilde expansion on datastore path
	cfg.Datastore.Path, err = u.TildeExpansion(cfg.Datastore.Path)
	if err != nil {
		return nil, err
	}

	return &cfg, err
}

// Set sets the value of a particular config key
func Set(filename, key, value string) error {
	return WriteConfigKey(filename, key, value)
}
