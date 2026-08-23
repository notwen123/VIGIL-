package cost

// On-chain verification for x402 payments.
//
// The replay guard in Redeem is necessary but nowhere near sufficient: it
// only proves a nonce has not been reused, not that anybody actually paid.
// Accepting a caller-supplied transaction hash without reading the chain
// would make the whole rail decorative — any agent could invent a hash and
// buy itself unlimited budget.
//
// So this reads the receipt and checks four things that all have to hold:
//
//	1. the transaction succeeded (status 1, not a reverted transfer);
//	2. it emitted an ERC-20 Transfer log from the *expected USDC contract*,
//	   not from an arbitrary token that happens to be named USDC;
//	3. the recipient is our configured address;
//	4. the amount is at least what the challenge quoted.
//
// Only then is the nonce redeemed.

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// transferTopic is keccak256("Transfer(address,address,uint256)"), the
// first topic of every ERC-20 Transfer event.
var transferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// usdcDecimals is 6 on Base, not the 18 that most ERC-20 tooling assumes.
// Getting this wrong by twelve orders of magnitude would either accept
// dust as full payment or reject real payments as insufficient.
const usdcDecimals = 6

// VerifyOnChain confirms a USDC payment and redeems the nonce.
//
// Returns the verified amount in whole USDC on success.
func (r *Rail) VerifyOnChain(ctx context.Context, nonce, txHash string) (float64, error) {
	if !r.Enabled() {
		return 0, fmt.Errorf("x402: no recipient configured")
	}
	rpc := os.Getenv("VIGIL_BASE_RPC_URL")
	if rpc == "" {
		// Refusing here is the honest outcome: without an RPC endpoint we
		// cannot check anything, and pretending otherwise would accept
		// fabricated payments.
		return 0, fmt.Errorf("x402: VIGIL_BASE_RPC_URL not set, cannot verify payment %s", txHash)
	}

	r.mu.Lock()
	challenge, known := r.issued[nonce]
	_, alreadyUsed := r.consumed[nonce]
	r.mu.Unlock()

	if alreadyUsed {
		return 0, fmt.Errorf("x402: nonce %s already redeemed", nonce)
	}
	if !known {
		return 0, fmt.Errorf("x402: unknown nonce %s", nonce)
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cl, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return 0, fmt.Errorf("x402: dial: %w", err)
	}
	defer cl.Close()

	receipt, err := cl.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		return 0, fmt.Errorf("x402: receipt for %s: %w", txHash, err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return 0, fmt.Errorf("x402: transaction %s reverted", txHash)
	}

	wantAsset := common.HexToAddress(r.Asset())
	wantTo := common.HexToAddress(r.recipient)

	quoted, err := parseUSDC(challenge.AmountUSDC)
	if err != nil {
		return 0, fmt.Errorf("x402: unparseable quote %q: %w", challenge.AmountUSDC, err)
	}

	for _, lg := range receipt.Logs {
		// Topic[0] identifies the event, [1] is `from`, [2] is `to`; a
		// Transfer always has exactly three topics.
		if len(lg.Topics) != 3 || lg.Topics[0] != transferTopic {
			continue
		}
		if lg.Address != wantAsset {
			// A Transfer from some other token contract. This is the check
			// that stops a worthless lookalike token being passed off as USDC.
			continue
		}
		to := common.BytesToAddress(lg.Topics[2].Bytes()[12:])
		if to != wantTo {
			continue
		}
		paid := new(big.Int).SetBytes(lg.Data)
		if paid.Cmp(quoted) < 0 {
			return 0, fmt.Errorf("x402: paid %s but %s USDC was quoted",
				formatUSDC(paid), challenge.AmountUSDC)
		}

		if err := r.Redeem(nonce, txHash); err != nil {
			return 0, err
		}
		amount, _ := new(big.Float).Quo(
			new(big.Float).SetInt(paid),
			big.NewFloat(1_000_000),
		).Float64()
		return amount, nil
	}

	return 0, fmt.Errorf(
		"x402: transaction %s contains no USDC transfer of >= %s to %s on chain %d",
		txHash, challenge.AmountUSDC, r.recipient, r.chainID)
}

// parseUSDC converts a decimal USDC string into base units (6 decimals),
// without going through float64 — money must not round.
func parseUSDC(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	// An empty amount must not parse. Left unguarded it becomes "000000",
	// i.e. a quote of zero, which every payment satisfies — a malformed or
	// missing amount would hand out free top-ups. Caught by TestParseUSDC.
	if s == "" {
		return nil, fmt.Errorf("empty amount")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > usdcDecimals {
		return nil, fmt.Errorf("more than %d decimal places", usdcDecimals)
	}
	// Guard the same hole on the whole part: ".50" would otherwise parse
	// through the concatenation below.
	if whole == "" {
		whole = "0"
	}
	frac += strings.Repeat("0", usdcDecimals-len(frac))

	n, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return nil, fmt.Errorf("not a decimal number")
	}
	if n.Sign() < 0 {
		return nil, fmt.Errorf("negative amount")
	}
	return n, nil
}

func formatUSDC(v *big.Int) string {
	f := new(big.Float).Quo(new(big.Float).SetInt(v), big.NewFloat(1_000_000))
	return f.Text('f', 2)
}
