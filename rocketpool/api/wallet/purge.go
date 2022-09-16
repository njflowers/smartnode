package wallet

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/rocket-pool/rocketpool-go/minipool"
	"github.com/rocket-pool/smartnode/shared/services"
	"github.com/rocket-pool/smartnode/shared/types/api"
	"github.com/rocket-pool/smartnode/shared/utils/validator"
	"github.com/urfave/cli"
)

func purge(c *cli.Context) (*api.PurgeResponse, error) {

	cfg, err := services.GetConfig(c)
	if err != nil {
		return nil, err
	}

	rp, err := services.GetRocketPool(c)
	if err != nil {
		return nil, err
	}

	w, err := services.GetWallet(c)
	if err != nil {
		return nil, err
	}

	pm, err := services.GetPasswordManager(c)
	if err != nil {
		return nil, err
	}

	// Get node account
	nodeAccount, err := w.GetNodeAccount()
	if err != nil {
		return nil, err
	}
	bc, err := services.GetBeaconClient(c)
	if err != nil {
		return nil, err
	}
	d, err := services.GetDocker(c)
	if err != nil {
		return nil, err
	}

	response := api.PurgeResponse{}

	// Get the node's validating pubkeys
	pubkeys, err := minipool.GetNodeValidatingMinipoolPubkeys(rp, nodeAccount.Address, nil)
	if err != nil {
		return nil, err
	}
	pubkeyMap := map[string]bool{}
	for _, pubkey := range pubkeys {
		pubkeyMap[pubkey.Hex()] = true
		// Delete the key
		w.DeleteValidatorKey(pubkey)
	}

	// Load custom validator keys
	customKeyDir := cfg.Smartnode.GetCustomKeyPath()
	info, err := os.Stat(customKeyDir)
	if os.IsNotExist(err) || !info.IsDir() {
		// There are no custom keys, so exit early
		return &response, nil
	}

	// Get the custom keystore files
	files, err := ioutil.ReadDir(customKeyDir)
	if err != nil {
		return nil, fmt.Errorf("error enumerating custom keystores: %w", err)
	}

	if len(files) == 0 {
		return &response, nil
	}

	// Process every custom key found
	for _, file := range files {
		// Read the file
		bytes, err := ioutil.ReadFile(filepath.Join(customKeyDir, file.Name()))
		if err != nil {
			return nil, fmt.Errorf("error reading custom keystore %s: %w", file.Name(), err)
		}

		// Deserialize it
		keystore := api.ValidatorKeystore{}
		err = json.Unmarshal(bytes, &keystore)
		if err != nil {
			return nil, fmt.Errorf("error deserializing custom keystore %s: %w", file.Name(), err)
		}

		// Check if it's one of the pubkeys for the minipool
		_, exists := pubkeyMap[keystore.Pubkey.Hex()]
		if !exists {
			// This pubkey isn't for any of this node's minipools so ignore it
			continue
		}
		customKeyPath := filepath.Join(customKeyDir, file.Name())
		err = os.RemoveAll(customKeyPath)
		if err != nil {
			return nil, fmt.Errorf("error removing file %s: %w", file.Name(), err)
		}
	}

	// Delete the wallet and password
	err = w.Delete()
	if err != nil {
		return nil, fmt.Errorf("error deleting wallet: %w", err)
	}
	err = pm.DeletePassword()
	if err != nil {
		return nil, fmt.Errorf("error deleting password: %w", err)
	}

	// Restart the VC once cleanup is done
	err = validator.RestartValidator(cfg, bc, nil, d)
	if err != nil {
		return nil, fmt.Errorf("error restarting validator client: %w", err)
	}

	return &response, nil
}