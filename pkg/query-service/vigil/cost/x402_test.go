package cost

import (
	"math/big"
	"testing"
)

// parseUSDC is a money path, so it is tested against the cases that
// actually cause losses: decimal truncation, the 6-vs-18 decimals trap, and
// anything that would silently accept dust as full payment.
func TestParseUSDC(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"1.00", 1_000_000, false},
		{"1", 1_000_000, false},
		{"0.01", 10_000, false},
		{"0.000001", 1, false}, // one base unit, the smallest payable amount
		{"12.345678", 12_345_678, false},
		{"100.5", 100_500_000, false},
		{" 2.50 ", 2_500_000, false}, // padded input from a header
		{"0", 0, false},

		// More precision than USDC has. Silently truncating this would let
		// a payer quote a sub-unit amount and have it rounded in their
		// favour, so it must be an error.
		{"1.0000001", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}

	for _, c := range cases {
		got, err := parseUSDC(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseUSDC(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUSDC(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got.Cmp(big.NewInt(c.want)) != 0 {
			t.Errorf("parseUSDC(%q) = %s, want %d", c.in, got, c.want)
		}
	}
}

// The 6-vs-18 decimals trap, stated as its own test because getting it
// wrong is a twelve-order-of-magnitude error: an 18-decimal assumption
// would treat a full 1 USDC payment as 0.000000000001 USDC and reject it,
// or treat dust as full payment depending on which side made the mistake.
func TestUSDCUsesSixDecimalsNotEighteen(t *testing.T) {
	oneUSDC, err := parseUSDC("1.00")
	if err != nil {
		t.Fatal(err)
	}
	if oneUSDC.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("1 USDC must be 1e6 base units on Base, got %s", oneUSDC)
	}
	if oneUSDC.Cmp(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) == 0 {
		t.Fatal("1 USDC was encoded with 18 decimals; Base USDC has 6")
	}
}

// Replay protection is the guard that stops one real payment buying many
// top-ups, so it is tested independently of any chain access.
func TestRedeemRejectsReplayAndUnknownNonce(t *testing.T) {
	r := &Rail{
		recipient: "0x000000000000000000000000000000000000dEaD",
		chainID:   84532,
		topUpUSDC: 1,
		issued:    map[string]Challenge{},
		consumed:  map[string]string{},
	}

	c, ok := r.Challenge("s-1", 0.25)
	if !ok {
		t.Fatal("expected a challenge to be issued")
	}
	if c.Nonce == "" {
		t.Fatal("challenge must carry a nonce")
	}

	if err := r.Redeem(c.Nonce, "0xtx1"); err != nil {
		t.Fatalf("first redemption should succeed: %v", err)
	}
	if err := r.Redeem(c.Nonce, "0xtx2"); err == nil {
		t.Fatal("replaying a redeemed nonce must fail — otherwise one payment buys unlimited budget")
	}
	if err := r.Redeem("never-issued", "0xtx3"); err == nil {
		t.Fatal("an unknown nonce must be rejected")
	}
}

// A rail with no configured payee must not issue challenges: quoting a
// price to an address nobody controls would strand the payer's money.
func TestDisabledRailIssuesNoChallenge(t *testing.T) {
	r := NewRailFromEnv() // no VIGIL_X402_RECIPIENT in the test env
	if r.Enabled() {
		t.Skip("VIGIL_X402_RECIPIENT is set in this environment")
	}
	if _, ok := r.Challenge("s-1", 1.0); ok {
		t.Fatal("a rail with no recipient must not issue a payment challenge")
	}
}
