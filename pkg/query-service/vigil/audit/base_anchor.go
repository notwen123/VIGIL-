package audit

// On-chain anchoring for the tamper-evident decision chain.
//
// The local ledger (ledger.go) makes tampering *detectable* by anyone who
// still holds an untampered copy. It cannot make tampering detectable to
// someone who only ever sees the operator's copy: a host-level adversary
// can rewrite the file and recompute every hash forward, and the result is
// internally consistent. That is the honest limitation stated in the
// whitepaper.
//
// Anchoring closes it. Periodically publishing a link of the chain to a
// public chain (Base) fixes that hash in a place the operator cannot
// rewrite, so any later divergence between the ledger and the anchor is
// provable by a third party. What goes on-chain is only the hash — never
// the tool, arguments, reason, or agent identity. The chain is a
// commitment, not a copy.
//
// Configuration, and what happens without it:
//
//	VIGIL_BASE_RPC_URL      Base RPC endpoint
//	VIGIL_BASE_PRIVATE_KEY  signer for the anchoring transaction
//	VIGIL_BASE_CONTRACT     deployed VigilAnchor address
//	VIGIL_BASE_CHAIN_ID     8453 mainnet, 84532 Sepolia (default 84532)
//
// With no private key configured this anchors nothing and says so once at
// startup. It does not fabricate a transaction hash, and it does not
// silently pretend the chain is anchored. An unanchored ledger is a
// documented state, not a hidden one.

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AnchorReceipt is the outcome of one anchoring attempt.
type AnchorReceipt struct {
	// TxHash is the real transaction hash, empty when nothing was sent.
	TxHash string `json:"tx_hash,omitempty"`
	// ExplorerURL is the Basescan link for TxHash, empty when unsent.
	ExplorerURL string `json:"explorer_url,omitempty"`
	AnchoredAt  string `json:"anchored_at,omitempty"`
	ChainID     int64  `json:"chain_id"`
	Hash        string `json:"hash"`
	PrevHash    string `json:"prev_hash"`
	// Sent distinguishes "anchored on chain" from "computed but not sent
	// because no signer is configured". Consumers must not present an
	// unsent anchor as on-chain evidence.
	Sent bool `json:"sent"`
	// Reason explains a non-send.
	Reason string `json:"reason,omitempty"`
}

// Anchorer publishes ledger hashes to Base.
type Anchorer struct {
	rpcURL     string
	contract   string
	chainID    int64
	privateKey *ecdsa.PrivateKey
	from       string
	logger     *slog.Logger

	once sync.Once
	mu   sync.Mutex
	// last holds the most recent receipt for the dashboard.
	last []AnchorReceipt
}

// anchorSelector is the 4-byte selector for
//
//	anchor(bytes32 decisionHash, bytes32 prevHash, uint64 timestamp)
//
// on the VigilAnchor contract (contracts/VigilAnchor.sol). Computed as the
// first four bytes of keccak256 of that signature.
const anchorSignature = "anchor(bytes32,bytes32,uint64)"

// NewAnchorerFromEnv builds an anchorer, or one that honestly does nothing.
func NewAnchorerFromEnv(logger *slog.Logger) *Anchorer {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Anchorer{
		rpcURL:   os.Getenv("VIGIL_BASE_RPC_URL"),
		contract: os.Getenv("VIGIL_BASE_CONTRACT"),
		chainID:  84532, // Base Sepolia unless told otherwise
		logger:   logger,
	}
	if v := os.Getenv("VIGIL_BASE_CHAIN_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			a.chainID = n
		}
	}
	if raw := strings.TrimPrefix(os.Getenv("VIGIL_BASE_PRIVATE_KEY"), "0x"); raw != "" {
		key, addr, err := parseKey(raw)
		if err != nil {
			// A malformed key is a configuration error worth shouting about;
			// it must not be mistaken for "anchoring is off".
			logger.Error("vigil: VIGIL_BASE_PRIVATE_KEY is set but unusable, anchoring disabled",
				slog.String("error", err.Error()))
		} else {
			a.privateKey, a.from = key, addr
		}
	}
	return a
}

// Enabled reports whether anchoring can actually send a transaction.
func (a *Anchorer) Enabled() bool {
	return a != nil && a.privateKey != nil && a.rpcURL != "" && a.contract != ""
}

// From returns the signer address, for display in the dashboard.
func (a *Anchorer) From() string {
	if a == nil {
		return ""
	}
	return a.from
}

// ChainID reports the configured chain.
func (a *Anchorer) ChainID() int64 {
	if a == nil {
		return 0
	}
	return a.chainID
}

// Anchor publishes one ledger link.
//
// Returns a receipt in every case. When no signer is configured the
// receipt carries Sent=false and a Reason, rather than an invented hash.
func (a *Anchorer) Anchor(ctx context.Context, decisionHash, prevHash string) (AnchorReceipt, error) {
	rec := AnchorReceipt{
		Hash:     decisionHash,
		PrevHash: prevHash,
		ChainID:  a.ChainID(),
	}
	if !a.Enabled() {
		rec.Reason = a.disabledReason()
		a.once.Do(func() {
			a.logger.Info("vigil: Base anchoring is not configured, ledger is local-only",
				slog.String("detail", rec.Reason),
				slog.String("to_enable", "set VIGIL_BASE_RPC_URL, VIGIL_BASE_PRIVATE_KEY, VIGIL_BASE_CONTRACT"))
		})
		a.remember(rec)
		return rec, nil
	}

	txHash, err := a.sendAnchorTx(ctx, decisionHash, prevHash)
	if err != nil {
		return rec, fmt.Errorf("vigil: base anchor failed: %w", err)
	}

	rec.TxHash = txHash
	rec.Sent = true
	rec.AnchoredAt = time.Now().UTC().Format(time.RFC3339)
	rec.ExplorerURL = a.explorer(txHash)

	a.logger.Info("vigil: decision anchored on Base",
		slog.String("tx", txHash),
		slog.String("explorer", rec.ExplorerURL),
		slog.Int64("chain_id", a.chainID),
		slog.String("decision_hash", decisionHash))

	a.remember(rec)
	return rec, nil
}

func (a *Anchorer) disabledReason() string {
	switch {
	case a == nil:
		return "anchorer not constructed"
	case a.privateKey == nil:
		return "VIGIL_BASE_PRIVATE_KEY not set"
	case a.rpcURL == "":
		return "VIGIL_BASE_RPC_URL not set"
	case a.contract == "":
		return "VIGIL_BASE_CONTRACT not set"
	}
	return ""
}

func (a *Anchorer) explorer(tx string) string {
	host := "https://sepolia.basescan.org"
	if a.chainID == 8453 {
		host = "https://basescan.org"
	}
	return host + "/tx/" + tx
}

func (a *Anchorer) remember(rec AnchorReceipt) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = append(a.last, rec)
	if len(a.last) > 50 {
		a.last = a.last[len(a.last)-50:]
	}
}

// Recent returns recent anchor receipts for the dashboard.
func (a *Anchorer) Recent() []AnchorReceipt {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AnchorReceipt, len(a.last))
	copy(out, a.last)
	return out
}

// calldata builds the ABI-encoded call: selector plus three 32-byte words.
func calldata(decisionHash, prevHash string, ts uint64) ([]byte, error) {
	sel := keccak([]byte(anchorSignature))[:4]

	d, err := word32(decisionHash)
	if err != nil {
		return nil, fmt.Errorf("decision hash: %w", err)
	}
	p, err := word32(prevHash)
	if err != nil {
		return nil, fmt.Errorf("prev hash: %w", err)
	}

	var t [32]byte
	big.NewInt(0).SetUint64(ts).FillBytes(t[:])

	out := make([]byte, 0, 4+96)
	out = append(out, sel...)
	out = append(out, d[:]...)
	out = append(out, p[:]...)
	out = append(out, t[:]...)
	return out, nil
}

// word32 left-pads a hex hash into a 32-byte ABI word. The genesis link
// has an empty prev hash, which encodes as 32 zero bytes.
func word32(h string) ([32]byte, error) {
	var w [32]byte
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return w, nil
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return w, fmt.Errorf("not hex: %w", err)
	}
	if len(b) > 32 {
		return w, fmt.Errorf("hash is %d bytes, want <= 32", len(b))
	}
	copy(w[32-len(b):], b)
	return w, nil
}
