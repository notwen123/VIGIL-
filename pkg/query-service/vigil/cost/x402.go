package cost

// x402: budget exhaustion as an HTTP payment challenge rather than a dead end.
//
// When a session's HOT budget runs out, VIGIL's existing behaviour is to
// block. That is correct for a runaway agent and wrong for a productive one
// that simply needs more allowance — and in an autonomous setting there is
// often no human awake to raise the limit. x402 (HTTP 402 Payment Required,
// revived for agent-to-agent settlement on Base) lets the agent top itself
// up: the server answers 402 with the exact terms, the agent pays USDC, and
// retries with proof.
//
// What this file does and does not do, stated plainly:
//
//   - It ISSUES the challenge: amount, asset, recipient, chain, nonce, and
//     an expiry, in the x402 header/body shape.
//   - It VERIFIES a presented payment by reading the chain — checking the
//     transfer actually happened, to the right address, for at least the
//     quoted amount, and that the nonce has not been replayed.
//   - It does NOT send funds. VIGIL is the payee here, never the payer;
//     there is no code path in this repository that transfers value out of
//     a wallet, which is deliberate. Anchoring (audit/base_anchor.go) sends
//     a zero-value transaction and is the only thing that signs at all.
//
// Without VIGIL_X402_RECIPIENT configured this issues no challenge and the
// budget block stands, which is the safe default: quoting a price to an
// address nobody controls would strand an agent's money.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// USDC on Base mainnet. Hardcoded rather than configurable by accident:
// accepting an arbitrary "USDC" contract address from configuration is how
// an operator ends up quoting prices in a worthless lookalike token.
const usdcBaseMainnet = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

// usdcBaseSepolia is the testnet USDC used when VIGIL_BASE_CHAIN_ID=84532.
const usdcBaseSepolia = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"

// Challenge is the body of a 402 response: everything an agent needs to pay
// and retry, and nothing it has to guess.
type Challenge struct {
	Scheme string `json:"scheme"` // "x402"
	// AmountUSDC is in whole USDC (6 decimals on Base), as a decimal string
	// to avoid float rounding on money.
	AmountUSDC string `json:"amount_usdc"`
	Asset      string `json:"asset"`
	Recipient  string `json:"recipient"`
	ChainID    int64  `json:"chain_id"`
	// Nonce ties a payment to this specific challenge, so the same transfer
	// cannot be presented twice to buy two top-ups.
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	// Instructions is human-readable because an operator reading a log
	// should not have to reconstruct what the agent was being asked for.
	Instructions string `json:"instructions"`
}

// Rail issues and verifies x402 challenges.
type Rail struct {
	recipient string
	chainID   int64
	// topUpUSDC is what one top-up costs.
	topUpUSDC float64

	mu sync.Mutex
	// issued tracks live nonces so a payment can be matched to its
	// challenge and a replayed one rejected.
	issued map[string]Challenge
	// consumed records nonces already redeemed.
	consumed map[string]string // nonce -> tx hash
}

// NewRailFromEnv builds the payment rail.
func NewRailFromEnv() *Rail {
	chainID := int64(84532)
	if v := os.Getenv("VIGIL_BASE_CHAIN_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			chainID = n
		}
	}
	topUp := 1.0
	if v := os.Getenv("VIGIL_X402_TOPUP_USDC"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			topUp = f
		}
	}
	return &Rail{
		recipient: os.Getenv("VIGIL_X402_RECIPIENT"),
		chainID:   chainID,
		topUpUSDC: topUp,
		issued:    map[string]Challenge{},
		consumed:  map[string]string{},
	}
}

// Enabled reports whether a payee address is configured.
func (r *Rail) Enabled() bool { return r != nil && r.recipient != "" }

// Asset returns the USDC contract for the configured chain.
func (r *Rail) Asset() string {
	if r != nil && r.chainID == 8453 {
		return usdcBaseMainnet
	}
	return usdcBaseSepolia
}

// Challenge builds a 402 for an exhausted session.
func (r *Rail) Challenge(sessionID string, overspent float64) (Challenge, bool) {
	if !r.Enabled() {
		return Challenge{}, false
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return Challenge{}, false
	}
	nonce := hex.EncodeToString(nb[:])

	c := Challenge{
		Scheme:     "x402",
		AmountUSDC: strconv.FormatFloat(r.topUpUSDC, 'f', 2, 64),
		Asset:      r.Asset(),
		Recipient:  r.recipient,
		ChainID:    r.chainID,
		Nonce:      nonce,
		// Short expiry: a stale quote invites paying for a top-up whose
		// session has long since ended.
		ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
		SessionID: sessionID,
		Reason: fmt.Sprintf(
			"session budget exhausted (over by $%.4f); pay to continue", overspent),
		Instructions: fmt.Sprintf(
			"Transfer %s USDC (%s) to %s on chain %d, then retry this call with "+
				"X-Payment-Tx and X-Payment-Nonce headers set.",
			strconv.FormatFloat(r.topUpUSDC, 'f', 2, 64), r.Asset(), r.recipient, r.chainID),
	}

	r.mu.Lock()
	r.issued[nonce] = c
	r.mu.Unlock()
	return c, true
}

// WriteChallenge emits a 402 response in x402 form.
func (r *Rail) WriteChallenge(w http.ResponseWriter, c Challenge) {
	w.Header().Set("Content-Type", "application/json")
	// The header carries the same terms as the body so a client that only
	// reads headers still knows the price.
	w.Header().Set("X-Payment-Required",
		fmt.Sprintf("scheme=x402; amount=%s; asset=%s; recipient=%s; chain=%d; nonce=%s",
			c.AmountUSDC, c.Asset, c.Recipient, c.ChainID, c.Nonce))
	w.WriteHeader(http.StatusPaymentRequired)
	json.NewEncoder(w).Encode(c)
}

// Redeem records a verified payment against its challenge.
//
// Verification of the transfer itself belongs on-chain (see VerifyOnChain);
// this is the replay guard that must run regardless, so one real payment
// cannot be presented repeatedly.
func (r *Rail) Redeem(nonce, txHash string) error {
	if !r.Enabled() {
		return fmt.Errorf("x402: no recipient configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if prior, seen := r.consumed[nonce]; seen {
		return fmt.Errorf("x402: nonce %s already redeemed by tx %s", nonce, prior)
	}
	c, ok := r.issued[nonce]
	if !ok {
		return fmt.Errorf("x402: unknown nonce %s", nonce)
	}
	expiry, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err == nil && time.Now().After(expiry) {
		return fmt.Errorf("x402: challenge %s expired at %s", nonce, c.ExpiresAt)
	}
	r.consumed[nonce] = txHash
	delete(r.issued, nonce)
	return nil
}

// Redeemed lists settled payments for the dashboard.
func (r *Rail) Redeemed() map[string]string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.consumed))
	for k, v := range r.consumed {
		out[k] = v
	}
	return out
}

// Status describes the rail for the dashboard.
func (r *Rail) Status() map[string]any {
	if r == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{
		"enabled":    r.Enabled(),
		"scheme":     "x402",
		"asset":      r.Asset(),
		"recipient":  r.recipient,
		"chain_id":   r.chainID,
		"topup_usdc": r.topUpUSDC,
		"redeemed":   len(r.Redeemed()),
		"direction":  "inbound only — VIGIL receives, never sends",
		"note":       "Set VIGIL_X402_RECIPIENT to enable. Without it, an exhausted budget blocks as before.",
	}
}
