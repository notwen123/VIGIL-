// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.24;

/// @title VigilAnchor
/// @notice Publishes links of VIGIL's tamper-evident decision chain to Base.
///
/// VIGIL's local ledger (pkg/query-service/vigil/audit/ledger.go) is a
/// SHA-256 hash chain over every firewall decision. It makes tampering
/// detectable to anyone holding an untampered copy — but not to someone who
/// only ever sees the operator's copy, because a host-level adversary can
/// rewrite the file and recompute every hash forward into something
/// internally consistent.
///
/// Anchoring closes that gap. Once a link is written here, the operator
/// cannot rewrite it, so a later divergence between the ledger and this
/// contract is provable by a third party who trusts neither.
///
/// Deliberately minimal. Only hashes are stored: never the tool, the
/// arguments, the reason, or the agent identity. A commitment, not a copy —
/// publishing agent behaviour to a public chain would be a privacy leak
/// dressed up as transparency.
contract VigilAnchor {
    /// @dev Emitted for every anchored link. Events rather than storage
    /// arrays: the chain of record lives off-chain, and logs are an order of
    /// magnitude cheaper than SSTORE for what is append-only evidence.
    event DecisionAnchored(
        address indexed anchor,
        bytes32 indexed decisionHash,
        bytes32 prevHash,
        uint64 decisionTimestamp,
        uint256 blockTimestamp
    );

    /// @notice Number of links anchored by each address.
    mapping(address => uint256) public anchorCount;

    /// @notice Most recent hash anchored by each address, so a verifier can
    /// confirm chain continuity without replaying every historical event.
    mapping(address => bytes32) public latestHash;

    /// @notice Anchor one link of the decision chain.
    /// @param decisionHash SHA-256 of the ledger event.
    /// @param prevHash     The preceding link. Zero for the genesis entry.
    /// @param timestamp    Decision time as recorded off-chain (unix seconds).
    ///
    /// @dev Permissionless by design. Anyone may anchor their own chain, and
    /// entries are namespaced by msg.sender, so one operator cannot pollute
    /// or overwrite another's history. There is no owner and no admin: an
    /// upgradeable or pausable anchor would defeat the purpose, since the
    /// operator could then suppress the very evidence being anchored.
    function anchor(bytes32 decisionHash, bytes32 prevHash, uint64 timestamp) external {
        require(decisionHash != bytes32(0), "VigilAnchor: empty decision hash");

        // Continuity check, skipped for the first entry from this sender.
        // This is what makes a *gap* detectable and not merely a rewrite:
        // an operator who deletes a decision cannot anchor the following one
        // without either presenting the deleted link or breaking the chain
        // here, on a ledger they do not control.
        bytes32 last = latestHash[msg.sender];
        if (last != bytes32(0)) {
            require(prevHash == last, "VigilAnchor: prevHash does not continue this chain");
        }

        latestHash[msg.sender] = decisionHash;
        unchecked {
            anchorCount[msg.sender] += 1;
        }

        emit DecisionAnchored(msg.sender, decisionHash, prevHash, timestamp, block.timestamp);
    }

    /// @notice Verify a claimed head against what was actually anchored.
    /// @dev The whole point of the contract: a third party checks the
    /// operator's claim against a value the operator cannot retroactively
    /// change.
    function verifyHead(address operator, bytes32 claimedHead) external view returns (bool) {
        return latestHash[operator] == claimedHead;
    }
}
