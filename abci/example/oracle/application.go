package oracle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	dbm "github.com/cometbft/cometbft-db"
	cryptoencoding "github.com/cometbft/cometbft/crypto/encoding"
	cryptoproto "github.com/cometbft/cometbft/proto/tendermint/crypto"

	code "github.com/cometbft/cometbft/abci/example/kvstore"
	"github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/version"
)

var (
	stateKey        = []byte("stateKey")
	kvPairPrefixKey = []byte("kvPairKey:")
)

const (
	ValidatorPrefix        = "val="
	AppVersion      uint64 = 1
)

type Application struct {
	types.BaseApplication

	state          State
	hashCount      int
	txCount        int
	serial         bool
	logger         log.Logger
	valUpdates     []types.ValidatorUpdate
	stagedTxs      [][]byte
	genBlockEvents bool

	valAddrToPubKeyMap map[string]cryptoproto.PublicKey
}

func NewApplication(serial bool) *Application {
	return &Application{
		logger:             log.NewNopLogger(),
		serial:             serial,
		state:              loadState(dbm.NewMemDB()),
		valAddrToPubKeyMap: make(map[string]cryptoproto.PublicKey),
	}
}

func (app *Application) Info(context.Context, *types.RequestInfo) (*types.ResponseInfo, error) {
	// Tendermint expects the application to persist validators, on start-up we need to reload them to memory if they exist
	if len(app.valAddrToPubKeyMap) == 0 && app.state.Height > 0 {
		validators := app.getValidators()
		for _, v := range validators {
			pubkey, err := cryptoencoding.PubKeyFromProto(v.PubKey)
			if err != nil {
				panic(fmt.Errorf("can't decode public key: %w", err))
			}
			app.valAddrToPubKeyMap[string(pubkey.Address())] = v.PubKey
		}
	}

	return &types.ResponseInfo{
		Data:             fmt.Sprintf("{\"size\":%v}", app.state.Size),
		Version:          version.ABCIVersion,
		AppVersion:       AppVersion,
		LastBlockHeight:  app.state.Height,
		LastBlockAppHash: app.state.Hash(),
	}, nil
}

func (app *Application) CheckTx(_ context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	var isOracleTx bool
	if app.serial {
		key, _, err := ParseTx(req.Tx)
		if err != nil {
			return nil, err
		}
		// convert Uint64
		keyUint, _ := strconv.ParseUint(key, 10, 64)

		isOracleTx = keyUint%2 == 0
	}

	return &types.ResponseCheckTx{Code: code.CodeTypeOK, IsOracleTx: isOracleTx}, nil
}

// FinalizeBlock executes the block against the application state. It punishes validators who equivocated and
// updates validators according to transactions in a block. The rest of the transactions are regular key value
// updates and are cached in memory and will be persisted once Commit is called.
// ConsensusParams are never changed.
func (app *Application) FinalizeBlock(_ context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	// reset valset changes
	app.valUpdates = make([]types.ValidatorUpdate, 0)
	app.stagedTxs = make([][]byte, 0)

	// Punish validators who committed equivocation.
	for _, ev := range req.Misbehavior {
		if ev.Type == types.MisbehaviorType_DUPLICATE_VOTE {
			addr := string(ev.Validator.Address)
			if pubKey, ok := app.valAddrToPubKeyMap[addr]; ok {
				app.valUpdates = append(app.valUpdates, types.ValidatorUpdate{
					PubKey: pubKey,
					Power:  ev.Validator.Power - 1,
				})
				app.logger.Info("Decreased val power by 1 because of the equivocation",
					"val", addr)
			} else {
				panic(fmt.Errorf("wanted to punish val %q but can't find it", addr))
			}
		}
	}

	respTxs := make([]*types.ExecTxResult, len(req.Txs))
	for i, tx := range req.Txs {
		if isValidatorTx(tx) {
			keyType, pubKey, power, err := parseValidatorTx(tx)
			if err != nil {
				panic(err)
			}
			app.valUpdates = append(app.valUpdates, types.UpdateValidator(pubKey, power, keyType))
		} else {
			app.stagedTxs = append(app.stagedTxs, tx)
		}

		var key, value string
		parts := bytes.Split(tx, []byte("="))
		if len(parts) == 2 {
			key, value = string(parts[0]), string(parts[1])
		} else {
			key, value = string(tx), string(tx)
		}
		respTxs[i] = &types.ExecTxResult{
			Code: code.CodeTypeOK,
			// With every transaction we can emit a series of events. To make it simple, we just emit the same events.
			Events: []types.Event{
				{
					Type: "app",
					Attributes: []types.EventAttribute{
						{Key: "creator", Value: "Cosmoshi Netowoko", Index: true},
						{Key: "key", Value: key, Index: true},
						{Key: "index_key", Value: "index is working", Index: true},
						{Key: "noindex_key", Value: "index is working", Index: false},
					},
				},
				{
					Type: "app",
					Attributes: []types.EventAttribute{
						{Key: "creator", Value: "Cosmoshi", Index: true},
						{Key: "key", Value: value, Index: true},
						{Key: "index_key", Value: "index is working", Index: true},
						{Key: "noindex_key", Value: "index is working", Index: false},
					},
				},
			},
		}
		app.state.Size++
	}

	app.state.Height = req.Height

	response := &types.ResponseFinalizeBlock{TxResults: respTxs, ValidatorUpdates: app.valUpdates, AppHash: app.state.Hash()}
	if !app.genBlockEvents {
		return response, nil
	}
	if app.state.Height%2 == 0 {
		response.Events = []types.Event{
			{
				Type: "begin_event",
				Attributes: []types.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "200",
						Index: true,
					},
				},
			},
			{
				Type: "begin_event",
				Attributes: []types.EventAttribute{
					{
						Key:   "foo",
						Value: "200",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "300",
						Index: true,
					},
				},
			},
		}
	} else {
		response.Events = []types.Event{
			{
				Type: "begin_event",
				Attributes: []types.EventAttribute{
					{
						Key:   "foo",
						Value: "400",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "300",
						Index: true,
					},
				},
			},
		}
	}
	return response, nil
}

func parseValidatorTx(tx []byte) (string, []byte, int64, error) {
	tx = tx[len(ValidatorPrefix):]

	//  get the pubkey and power
	typeKeyAndPower := strings.Split(string(tx), "!")
	if len(typeKeyAndPower) != 3 {
		return "", nil, 0, fmt.Errorf("expected 'pubkeytype!pubkey!power'. Got %v", typeKeyAndPower)
	}
	keytype, pubkeyS, powerS := typeKeyAndPower[0], typeKeyAndPower[1], typeKeyAndPower[2]

	// decode the pubkey
	pubkey, err := base64.StdEncoding.DecodeString(pubkeyS)
	if err != nil {
		return "", nil, 0, fmt.Errorf("pubkey (%s) is invalid base64", pubkeyS)
	}

	// decode the power
	power, err := strconv.ParseInt(powerS, 10, 64)
	if err != nil {
		return "", nil, 0, fmt.Errorf("power (%s) is not an int", powerS)
	}

	if power < 0 {
		return "", nil, 0, fmt.Errorf("power can not be less than 0, got %d", power)
	}

	return keytype, pubkey, power, nil
}

// PrepareProposal is called when the node is a proposer. CometBFT stages a set of transactions to the application. As the
// KVStore has two accepted formats, `:` and `=`, we modify all instances of `:` with `=` to make it consistent. Note: this is
// quite a trivial example of transaction modification.
// NOTE: we assume that CometBFT will never provide more transactions than can fit in a block.
func (app *Application) PrepareProposal(ctx context.Context, req *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	return &types.ResponsePrepareProposal{Txs: app.formatTxs(ctx, req.Txs)}, nil
}

// formatTxs validates and excludes invalid transactions
// also substitutes all the transactions with x:y to x=y
func (app *Application) formatTxs(ctx context.Context, blockData [][]byte) [][]byte {
	txs := make([][]byte, 0, len(blockData))
	for _, tx := range blockData {
		if resp, err := app.CheckTx(ctx, &types.RequestCheckTx{Tx: tx}); err == nil && resp.Code == code.CodeTypeOK {
			txs = append(txs, bytes.Replace(tx, []byte(":"), []byte("="), 1))
		}
	}
	return txs
}

func (app *Application) Commit(ctx context.Context, req *types.RequestCommit) (resp *types.ResponseCommit, err error) {
	app.hashCount++
	if app.txCount == 0 {
		return &types.ResponseCommit{}, nil
	}
	hash := make([]byte, 8)
	binary.BigEndian.PutUint64(hash, uint64(app.txCount))
	return &types.ResponseCommit{}, nil
}

func (app *Application) Query(ctx context.Context, reqQuery *types.RequestQuery) (*types.ResponseQuery, error) {
	switch reqQuery.Path {
	case "hash":
		return &types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.hashCount))}, nil
	case "tx":
		return &types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.txCount))}, nil
	default:
		return &types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected hash or tx, got %v", reqQuery.Path)}, nil
	}
}

// parseTx parses a tx in 'key=value' format into a key and value.
func ParseTx(tx []byte) (string, string, error) {
	parts := bytes.Split(tx, []byte("="))
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid tx format: %q", string(tx))
	}
	if len(parts[0]) == 0 {
		return "", "", errors.New("key cannot be empty")
	}
	return string(parts[0]), string(parts[1]), nil
}

func isValidatorTx(tx []byte) bool {
	return strings.HasPrefix(string(tx), ValidatorPrefix)
}

func (app *Application) getValidators() (validators []types.ValidatorUpdate) {
	itr, err := app.state.db.Iterator(nil, nil)
	if err != nil {
		panic(err)
	}
	for ; itr.Valid(); itr.Next() {
		if isValidatorTx(itr.Key()) {
			validator := new(types.ValidatorUpdate)
			err := types.ReadMessage(bytes.NewBuffer(itr.Value()), validator)
			if err != nil {
				panic(err)
			}
			validators = append(validators, *validator)
		}
	}
	if err = itr.Error(); err != nil {
		panic(err)
	}
	return
}

// -----------------------------

type State struct {
	db dbm.DB
	// Size is essentially the amount of transactions that have been processes.
	// This is used for the appHash
	Size   int64 `json:"size"`
	Height int64 `json:"height"`
}

func loadState(db dbm.DB) State {
	var state State
	state.db = db
	stateBytes, err := db.Get(stateKey)
	if err != nil {
		panic(err)
	}
	if len(stateBytes) == 0 {
		return state
	}
	err = json.Unmarshal(stateBytes, &state)
	if err != nil {
		panic(err)
	}
	return state
}

func saveState(state State) {
	stateBytes, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	err = state.db.Set(stateKey, stateBytes)
	if err != nil {
		panic(err)
	}
}

// Hash returns the hash of the application state. This is computed
// as the size or number of transactions processed within the state. Note that this isn't
// a strong guarantee of state machine replication because states could
// have different kv values but still have the same size.
// This function is used as the "AppHash"
func (s State) Hash() []byte {
	appHash := make([]byte, 8)
	binary.PutVarint(appHash, s.Size)
	return appHash
}

func prefixKey(key []byte) []byte {
	return append(kvPairPrefixKey, key...)
}
