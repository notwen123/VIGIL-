package audit

// The signing and transport half of Base anchoring.
//
// This uses go-ethereum's own primitives rather than a hand-rolled
// secp256k1/RLP implementation. Rolling your own transaction signing is a
// way to lose funds to a subtle encoding bug, and the anchoring value here
// comes from the signature being verifiable by everyone else's tooling —
// which means it has to be byte-identical to what the canonical library
// produces.

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// keccak256 over arbitrary bytes, used for the ABI function selector.
func keccak(b []byte) []byte { return crypto.Keccak256(b) }

// parseKey turns a hex private key into a signer and its address.
func parseKey(hexKey string) (*ecdsa.PrivateKey, string, error) {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(hexKey, "0x"))
	if err != nil {
		return nil, "", err
	}
	return key, crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}

// sendAnchorTx submits one anchoring transaction and returns its hash.
//
// EIP-1559 typed transaction: Base is an OP-stack chain and legacy pricing
// there tends to either overpay or get stuck behind the basefee.
func (a *Anchorer) sendAnchorTx(ctx context.Context, decisionHash, prevHash string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cl, err := ethclient.DialContext(ctx, a.rpcURL)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", a.rpcURL, err)
	}
	defer cl.Close()

	// Refuse to sign for a chain other than the configured one. Without
	// this a misconfigured RPC URL would silently anchor to the wrong
	// network, producing a receipt that looks valid and proves nothing.
	onChainID, err := cl.ChainID(ctx)
	if err != nil {
		return "", fmt.Errorf("chain id: %w", err)
	}
	if onChainID.Int64() != a.chainID {
		return "", fmt.Errorf(
			"refusing to sign: RPC reports chain %d, configured VIGIL_BASE_CHAIN_ID is %d",
			onChainID.Int64(), a.chainID)
	}

	from := crypto.PubkeyToAddress(a.privateKey.PublicKey)
	to := common.HexToAddress(a.contract)

	data, err := calldata(decisionHash, prevHash, uint64(time.Now().Unix()))
	if err != nil {
		return "", err
	}

	nonce, err := cl.PendingNonceAt(ctx, from)
	if err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	tipCap, err := cl.SuggestGasTipCap(ctx)
	if err != nil {
		// Some OP-stack RPCs do not implement the tip endpoint; a small
		// fixed tip is fine on Base, where priority fees are negligible.
		tipCap = big.NewInt(1_000_000) // 0.001 gwei
	}
	head, err := cl.HeaderByNumber(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("head: %w", err)
	}
	// Standard 2x basefee headroom so the tx survives a basefee rise
	// between estimation and inclusion.
	feeCap := new(big.Int).Add(tipCap, new(big.Int).Mul(head.BaseFee, big.NewInt(2)))

	gas, err := cl.EstimateGas(ctx, ethereum.CallMsg{
		From: from, To: &to, Data: data,
	})
	if err != nil {
		return "", fmt.Errorf("estimate gas (is %s a VigilAnchor contract on chain %d?): %w",
			a.contract, a.chainID, err)
	}
	// Estimation is a lower bound; a 25% buffer avoids out-of-gas on a
	// storage slot that turns out to be cold.
	gas = gas * 125 / 100

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(a.chainID),
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       gas,
		To:        &to,
		Value:     big.NewInt(0), // anchoring never transfers value
		Data:      data,
	})

	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(a.chainID)), a.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	if err := cl.SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	return signed.Hash().Hex(), nil
}
