package watchtower

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/rocket-pool/rocketpool-go/rewards"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/smartnode/shared/services"
	"github.com/rocket-pool/smartnode/shared/services/beacon"
	"github.com/rocket-pool/smartnode/shared/services/config"
	rprewards "github.com/rocket-pool/smartnode/shared/services/rewards"
	"github.com/rocket-pool/smartnode/shared/utils/log"
	"github.com/urfave/cli"
)

// Generate rewards Merkle Tree task
type generateRewardsTree struct {
	c         *cli.Context
	log       log.ColorLogger
	errLog    log.ColorLogger
	cfg       *config.RocketPoolConfig
	rp        *rocketpool.RocketPool
	ec        rocketpool.ExecutionClient
	bc        beacon.Client
	lock      *sync.Mutex
	isRunning bool
}

// Create generate rewards Merkle Tree task
func newGenerateRewardsTree(c *cli.Context, logger log.ColorLogger, errorLogger log.ColorLogger) (*generateRewardsTree, error) {

	// Get services
	cfg, err := services.GetConfig(c)
	if err != nil {
		return nil, err
	}
	ec, err := services.GetEthClient(c)
	if err != nil {
		return nil, err
	}
	bc, err := services.GetBeaconClient(c)
	if err != nil {
		return nil, err
	}
	rp, err := services.GetRocketPool(c)
	if err != nil {
		return nil, err
	}

	lock := &sync.Mutex{}
	generator := &generateRewardsTree{
		c:         c,
		log:       logger,
		errLog:    errorLogger,
		cfg:       cfg,
		ec:        ec,
		bc:        bc,
		rp:        rp,
		lock:      lock,
		isRunning: false,
	}

	return generator, nil
}

// Check for generation requests
func (t *generateRewardsTree) run() error {
	t.log.Println("Checking for manual rewards tree generation requests...")

	// Check if rewards generation is already running
	t.lock.Lock()
	if t.isRunning {
		t.log.Println("Tree generation is already running.")
		t.lock.Unlock()
		return nil
	}
	t.lock.Unlock()

	// Check for requests
	requestDir := t.cfg.Smartnode.GetWatchtowerFolder(true)
	files, err := ioutil.ReadDir(requestDir)
	if err != nil {
		return fmt.Errorf("Error enumerating files in watchtower storage directory: %w", err)
	}

	for _, file := range files {
		filename := file.Name()
		if strings.HasSuffix(filename, config.RegenerateRewardsTreeRequestSuffix) && !file.IsDir() {
			// Get the index
			indexString := strings.TrimSuffix(filename, config.RegenerateRewardsTreeRequestSuffix)
			index, err := strconv.ParseUint(indexString, 0, 64)
			if err != nil {
				return fmt.Errorf("Error parsing index from [%s]: %w", filename, err)
			}

			// Delete the file
			path := filepath.Join(requestDir, filename)
			err = os.Remove(path)
			if err != nil {
				return fmt.Errorf("Error removing request file [%s]: %w", path, err)
			}

			// Generate the rewards tree
			t.lock.Lock()
			t.isRunning = true
			t.lock.Unlock()
			go t.generateRewardsTree(index)

			// Return after the first request, do others at other intervals
			return nil
		}
	}

	return nil
}

func (t *generateRewardsTree) generateRewardsTree(index uint64) {
	// Begin generation of the tree
	generationPrefix := fmt.Sprintf("[Interval %d Tree]", index)
	t.log.Printlnf("%s Starting generation of Merkle rewards tree for interval %d.", generationPrefix, index)

	// Get the event log interval
	eventLogInterval, err := t.cfg.GetEventLogInterval()
	if err != nil {
		t.handleError(fmt.Errorf("%s Error getting event log interval: %w", generationPrefix, err))
		return
	}

	// Find the event for this interval
	rewardsEvent, err := rewards.GetRewardSnapshotEvent(t.rp, index, big.NewInt(int64(eventLogInterval)), nil)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error getting event for interval %d: %w", generationPrefix, index, err))
		return
	}
	t.log.Printlnf("%s Found snapshot event: consensus block %s", generationPrefix, rewardsEvent.Block.String())

	// Figure out the timestamp for the block
	eth2Config, err := t.bc.GetEth2Config()
	if err != nil {
		t.handleError(fmt.Errorf("%s Error getting Beacon config: %w", generationPrefix, err))
		return
	}
	genesisTime := time.Unix(int64(eth2Config.GenesisTime), 0)
	blockTime := genesisTime.Add(time.Duration(rewardsEvent.Block.Uint64()*eth2Config.SecondsPerSlot) * time.Second)
	t.log.Printlnf("%s Block time is %s", generationPrefix, blockTime)

	// Get the matching EL block
	elBlockHeader, err := rprewards.GetELBlockHeaderForTime(blockTime, t.ec)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error getting matching EL block: %w", generationPrefix, err))
		return
	}
	t.log.Printlnf("%s Found matching EL block: %s", generationPrefix, elBlockHeader.Number.String())

	// Get the interval time
	intervalTime := rewardsEvent.IntervalEndTime.Sub(rewardsEvent.IntervalStartTime)

	// Get the total pending rewards and respective distribution percentages
	t.log.Printlnf("%s Calculating RPL rewards...", generationPrefix)
	start := time.Now()
	nodeRewardsMap, networkRewardsMap, invalidNodeNetworks, err := rprewards.CalculateRplRewards(t.rp, elBlockHeader, intervalTime)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error calculating node operator rewards: %w", generationPrefix, err))
		return
	}
	for address, network := range invalidNodeNetworks {
		t.log.Printlnf("%s WARNING: Node %s has invalid network %d assigned!\n", generationPrefix, address.Hex(), network)
	}
	t.log.Printlnf("%s Finished in %s", generationPrefix, time.Since(start).String())

	// Generate the Merkle tree
	t.log.Printlnf("%s Generating Merkle tree...", generationPrefix)
	start = time.Now()
	tree, err := rprewards.GenerateMerkleTree(nodeRewardsMap)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error generating Merkle tree: %w", generationPrefix, err))
		return
	}
	t.log.Printlnf("%s Finished in %s", generationPrefix, time.Since(start).String())

	// Validate the Merkle root
	root := common.BytesToHash(tree.Root())
	if root != rewardsEvent.MerkleRoot {
		t.log.Printlnf("%s WARNING: your Merkle tree had a root of %s, but the canonical Merkle tree's root was %s. This file will not be usable for claiming rewards.", generationPrefix, root.Hex(), rewardsEvent.MerkleRoot.Hex())
	} else {
		t.log.Printlnf("%s Your Merkle tree's root of %s matches the canonical root! You will be able to use this file for claiming rewards.", generationPrefix, hexutil.Encode(tree.Root()))
	}

	// Create the JSON proof wrapper and encode it
	t.log.Printlnf("%s Saving JSON file...", generationPrefix)
	proofWrapper := rprewards.GenerateTreeJson(tree.Root(), nodeRewardsMap, networkRewardsMap)
	wrapperBytes, err := json.Marshal(proofWrapper)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error serializing proof wrapper into JSON: %w", generationPrefix, err))
		return
	}

	// Write the file
	path := t.cfg.Smartnode.GetRewardsTreePath(index, true)
	err = ioutil.WriteFile(path, wrapperBytes, 0644)
	if err != nil {
		t.handleError(fmt.Errorf("%s Error saving file to %s: %w", generationPrefix, path, err))
		return
	}

	t.log.Printlnf("%s Merkle tree generation complete!", generationPrefix)
	t.lock.Lock()
	t.isRunning = false
	t.lock.Unlock()
}

func (t *generateRewardsTree) handleError(err error) {
	t.errLog.Println(err)
	t.errLog.Println("*** Rewards tree generation failed. ***")
	t.lock.Lock()
	t.isRunning = false
	t.lock.Unlock()
}