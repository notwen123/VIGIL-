#!/usr/bin/env bash
# Proves VIGIL's Base anchoring path end to end — against a real EVM chain,
# with real transaction hashes, without spending anything.
#
#   ./demo/anchor_proof.sh
#
# Requires foundry (anvil, forge, cast). Runs a local chain configured with
# Base Sepolia's chain id (84532), deploys the real VigilAnchor.sol, and
# drives it through VIGIL's own Go anchoring code — not a mock, not a
# hand-written transaction.
#
# What this proves, and what it does not:
#
#   PROVES   the calldata encoding, EIP-1559 signing, chain-id guard, gas
#            estimation and contract logic are all correct, because a real
#            node accepted the transactions and the contract's state
#            changed accordingly.
#
#   DOES NOT prove anything about public Base. These hashes exist only on
#            the local chain this script starts. The explorer URLs VIGIL
#            prints are derived from the chain id, so they point at
#            sepolia.basescan.org and will NOT resolve — that is expected
#            here and is exactly why the production status endpoint reports
#            anchoring_enabled=false until a funded signer is configured.

set -euo pipefail
cd "$(dirname "$0")/.."

export PATH="$HOME/.foundry/bin:$PATH"
for bin in anvil forge cast; do
  command -v "$bin" >/dev/null || { echo "missing $bin — install foundry: https://getfoundry.sh"; exit 1; }
done

RPC=http://127.0.0.1:8545
CHAIN=84532
# anvil's first well-known development account. Public, worthless, and
# never used anywhere but a local chain.
KEY=0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
SIGNER=0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266

rule() { printf '%s\n' "──────────────────────────────────────────────────────────────"; }

echo "VIGIL — Base anchoring proof"
rule

anvil --port 8545 --chain-id $CHAIN --silent >/tmp/anvil-proof.log 2>&1 &
ANVIL=$!
trap 'kill $ANVIL 2>/dev/null || true' EXIT
for _ in $(seq 1 40); do cast chain-id --rpc-url $RPC >/dev/null 2>&1 && break; sleep 0.25; done
echo "[1] local chain up, chain-id $(cast chain-id --rpc-url $RPC)"

WORK=$(mktemp -d)
mkdir -p "$WORK/src"
cp contracts/VigilAnchor.sol "$WORK/src/"
printf '[profile.default]\nsrc = "src"\nout = "out"\nsolc = "0.8.24"\n' > "$WORK/foundry.toml"
( cd "$WORK" && forge build >/dev/null 2>&1 )
CONTRACT=$( cd "$WORK" && forge create src/VigilAnchor.sol:VigilAnchor \
  --rpc-url $RPC --private-key $KEY --broadcast 2>/dev/null | awk '/Deployed to:/{print $3}' )
echo "[2] VigilAnchor deployed at $CONTRACT"

echo "[3] anchoring two ledger links through VIGIL's own Go code"
cat > "$WORK/anchor.go" <<'GOEOF'
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
)

func main() {
	a := audit.NewAnchorerFromEnv(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !a.Enabled() {
		fmt.Println("ERROR: anchorer not enabled")
		return
	}
	h1 := "1111111111111111111111111111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222222222222222222222222222"
	r1, err1 := a.Anchor(context.Background(), h1, "")
	r2, err2 := a.Anchor(context.Background(), h2, h1)
	fmt.Printf("TX1=%s ERR1=%v\n", r1.TxHash, err1)
	fmt.Printf("TX2=%s ERR2=%v\n", r2.TxHash, err2)
}
GOEOF

OUT=$(VIGIL_BASE_RPC_URL=$RPC VIGIL_BASE_PRIVATE_KEY=$KEY \
      VIGIL_BASE_CONTRACT=$CONTRACT VIGIL_BASE_CHAIN_ID=$CHAIN \
      go run "$WORK/anchor.go")
TX1=$(echo "$OUT" | awk -F'[= ]' '/^TX1/{print $2}')
TX2=$(echo "$OUT" | awk -F'[= ]' '/^TX2/{print $2}')
echo "    tx1 $TX1"
echo "    tx2 $TX2"

echo "[4] verifying on chain"
S1=$(cast receipt "$TX1" --rpc-url $RPC 2>/dev/null | awk '/^status/{print $2}')
GAS=$(cast receipt "$TX1" --rpc-url $RPC 2>/dev/null | awk '/^gasUsed/{print $2}')
COUNT=$(cast call "$CONTRACT" 'anchorCount(address)(uint256)' $SIGNER --rpc-url $RPC)
HEAD=$(cast call "$CONTRACT" 'latestHash(address)(bytes32)' $SIGNER --rpc-url $RPC)
echo "    receipt status  : $S1   gas $GAS"
echo "    anchorCount     : $COUNT"
echo "    latestHash      : $HEAD"

GOOD=$(cast call "$CONTRACT" 'verifyHead(address,bytes32)(bool)' $SIGNER \
  0x2222222222222222222222222222222222222222222222222222222222222222 --rpc-url $RPC)
BAD=$(cast call "$CONTRACT" 'verifyHead(address,bytes32)(bool)' $SIGNER \
  0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddeaddead --rpc-url $RPC)
echo "    verifyHead real : $GOOD"
echo "    verifyHead fake : $BAD"

echo "[5] tamper guard — anchoring a link that skips a decision"
if cast send "$CONTRACT" 'anchor(bytes32,bytes32,uint64)' \
     0x3333333333333333333333333333333333333333333333333333333333333333 \
     0x9999999999999999999999999999999999999999999999999999999999999999 \
     1700000000 --rpc-url $RPC --private-key $KEY >/dev/null 2>&1; then
  echo "    FAIL: a broken chain was accepted"
  exit 1
fi
echo "    reverted as expected (prevHash does not continue this chain)"
AFTER=$(cast call "$CONTRACT" 'latestHash(address)(bytes32)' $SIGNER --rpc-url $RPC)
[ "$AFTER" = "$HEAD" ] || { echo "    FAIL: head moved despite revert"; exit 1; }
echo "    head unchanged after the rejected attempt"

rule
if [ "$S1" = "1" ] && [ "$COUNT" = "2" ] && [ "$GOOD" = "true" ] && [ "$BAD" = "false" ]; then
  echo "ANCHOR PROOF PASSED — real transactions, real contract state."
  echo "These hashes exist on the local chain only; public Base anchoring"
  echo "still requires a funded signer, and the status endpoint says so."
  exit 0
fi
echo "ANCHOR PROOF FAILED"
exit 1
