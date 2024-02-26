package oracle

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cometbft/cometbft/abci/example"
	"github.com/cometbft/cometbft/abci/types"
)

type Application struct {
	types.BaseApplication
}

func (app *Application) CheckTx(_ context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	// If it is a validator update transaction, check that it is correctly formatted
	if len(req.Tx) > 8 {
		return &types.ResponseCheckTx{
			Code: example.CodeTypeEncodingError,
			Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(req.Tx))}, nil
	}

	tx8 := make([]byte, 8)
	copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
	txValue := binary.BigEndian.Uint64(tx8)
	// even numbers are oracle tx
	isOracleTx := txValue%2 == 0
	return &types.ResponseCheckTx{Code: example.CodeTypeOK, IsOracleTx: isOracleTx}, nil
}

func (app *Application) ProcessProposal(ctx context.Context, req *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	for _, tx := range req.Txs {
		// As CheckTx is a full validity check we can simply reuse this
		if resp, err := app.CheckTx(ctx, &types.RequestCheckTx{Tx: tx}); err != nil || resp.Code != example.CodeTypeOK {
			return &types.ResponseProcessProposal{Status: types.ResponseProcessProposal_REJECT}, nil
		}
	}
	return &types.ResponseProcessProposal{Status: types.ResponseProcessProposal_ACCEPT}, nil
}
