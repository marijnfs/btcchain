// Copyright (c) 2013 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcchain

import (
	"fmt"
	"github.com/conformal/btcdb"
	"github.com/conformal/btcutil"
	"github.com/conformal/btcwire"
)

// txData contains contextual information about transactions such as which block
// they were found in and whether or not the outputs are spent.
type txData struct {
	tx          *btcwire.MsgTx
	hash        *btcwire.ShaHash
	blockHeight int64
	spent       []bool
	err         error
}

// connectTransactions updates the passed map by applying transaction and
// spend information for all the transactions in the passed block. Only
// transactions in the passed map are updated.
func connectTransactions(txStore map[btcwire.ShaHash]*txData, block *btcutil.Block) error {
	// Loop through all of the transactions in the block to see if any of
	// them are ones we need to update and spend based on the results map.
	for i, tx := range block.MsgBlock().Transactions {
		txHash, err := block.TxSha(i)
		if err != nil {
			return err
		}

		// Update the transaction store with the transaction information
		// if it's one of the requested transactions.
		if txD, exists := txStore[*txHash]; exists {
			txD.tx = tx
			txD.blockHeight = block.Height()
			txD.spent = make([]bool, len(tx.TxOut))
			txD.err = nil
		}

		// Spend the origin transaction output.
		for _, txIn := range tx.TxIn {
			originHash := &txIn.PreviousOutpoint.Hash
			originIndex := txIn.PreviousOutpoint.Index
			if originTx, exists := txStore[*originHash]; exists {
				originTx.spent[originIndex] = true
			}
		}
	}

	return nil
}

// disconnectTransactions updates the passed map by undoing transaction and
// spend information for all transactions in the passed block.  Only
// transactions in the passed map are updated.
func disconnectTransactions(txStore map[btcwire.ShaHash]*txData, block *btcutil.Block) error {
	// Loop through all of the transactions in the block to see if any of
	// them are ones were need to undo based on the results map.
	for i, tx := range block.MsgBlock().Transactions {
		txHash, err := block.TxSha(i)
		if err != nil {
			return err
		}

		// Remove this transaction from the transaction store (this is a
		// no-op if it's not there).
		delete(txStore, *txHash)

		// Unspend the origin transaction output.
		for _, txIn := range tx.TxIn {
			originHash := &txIn.PreviousOutpoint.Hash
			originIndex := txIn.PreviousOutpoint.Index
			if originTx, exists := txStore[*originHash]; exists {
				originTx.spent[originIndex] = false
			}
		}
	}

	return nil
}

// fetchTxList fetches transaction data about the provided list of transactions
// from the point of view of the given node.  For example, a given node might
// be down a side chain where a transaction hasn't been spent from its point of
// view even though it might have been spent in the main chain (or another side
// chain).  Another scenario is where a transaction exists from the point of
// view of the main chain, but doesn't exist in a side chain that branches
// before the block that contains the transaction on the main chain.
func (b *BlockChain) fetchTxList(node *blockNode, txList []*btcwire.ShaHash) (map[btcwire.ShaHash]*txData, error) {
	// Get the previous block node.  This function is used over simply
	// accessing node.parent directly as it will dynamically create previous
	// block nodes as needed.  This helps allow only the pieces of the chain
	// that are needed to remain in memory.
	prevNode, err := b.getPrevNodeFromNode(node)
	if err != nil {
		return nil, err
	}

	// The transaction store map needs to have an entry for every requested
	// transaction.  By default, all the transactions are marked as missing.
	// Each entry will be filled in with the appropriate data below.
	txStore := make(map[btcwire.ShaHash]*txData)
	for _, hash := range txList {
		txStore[*hash] = &txData{hash: hash, err: btcdb.TxShaMissing}
	}

	// Ask the database (main chain) for the list of transactions.  This
	// will return the information from the point of view of the end of the
	// main chain.
	txReplyList := b.db.FetchTxByShaList(txList)
	for _, txReply := range txReplyList {
		// Lookup the existing results entry to modify.  Skip
		// this reply if there is no corresponding entry in
		// the transaction store map which really should not happen, but
		// be safe.
		txD, ok := txStore[*txReply.Sha]
		if !ok {
			continue
		}

		// Fill in the transaction details.  A copy is used here since
		// there is no guarantee the returned data isn't cached and
		// this code modifies the data.  A bug caused by modifying the
		// cached data would likely be difficult to track down and could
		// cause subtle errors, so avoid the potential altogether.
		txD.err = txReply.Err
		if txReply.Err == nil {
			txD.tx = txReply.Tx
			txD.blockHeight = txReply.Height
			txD.spent = make([]bool, len(txReply.TxSpent))
			copy(txD.spent, txReply.TxSpent)
		}
	}

	// At this point, we have the transaction data from the point of view
	// of the end of the main (best) chain.  If we haven't selected a best
	// chain yet or we are extending the main (best) chain with a new block,
	// everything is accurate, so return the results now.
	if b.bestChain == nil || (prevNode != nil && prevNode.hash.IsEqual(b.bestChain.hash)) {
		return txStore, nil
	}

	// The requested node is either on a side chain or is a node on the main
	// chain before the end of it.  In either case, we need to undo the
	// transactions and spend information for the blocks which would be
	// disconnected during a reorganize to the point of view of the
	// node just before the requested node.
	detachNodes, attachNodes := b.getReorganizeNodes(prevNode)
	for e := detachNodes.Front(); e != nil; e = e.Next() {
		n := e.Value.(*blockNode)
		block, err := b.db.FetchBlockBySha(n.hash)
		if err != nil {
			return nil, err
		}

		disconnectTransactions(txStore, block)
	}

	// The transaction store is now accurate to either the node where the
	// requested node forks off the main chain (in the case where the
	// requested node is on a side chain), or the requested node itself if
	// the requested node is an old node on the main chain.  Entries in the
	// attachNodes list indicate the requested node is on a side chain, so
	// if there are no nodes to attach, we're done.
	if attachNodes.Len() == 0 {
		return txStore, nil
	}

	// The requested node is on a side chain, so we need to apply the
	// transactions and spend information from each of the nodes to attach.
	for e := attachNodes.Front(); e != nil; e = e.Next() {
		n := e.Value.(*blockNode)
		block, exists := b.blockCache[*n.hash]
		if !exists {
			return nil, fmt.Errorf("unable to find block %v in "+
				"side chain cache for transaction search",
				n.hash)
		}

		connectTransactions(txStore, block)
	}

	return txStore, nil
}

// fetchInputTransactions fetches the input transactions referenced by the
// transactions in the given block from its point of view.  See fetchTxList
// for more details on what the point of view entails.
func (b *BlockChain) fetchInputTransactions(node *blockNode, block *btcutil.Block) (map[btcwire.ShaHash]*txData, error) {
	// Build a map of in-flight transactions because some of the inputs in
	// this block could be referencing other transactions in this block
	// which are not yet in the chain.
	txInFlight := map[btcwire.ShaHash]*btcwire.MsgTx{}
	for i, tx := range block.MsgBlock().Transactions {
		// Get transaction hash.  It's safe to ignore the error since
		// it's already cached in the nominal code path and the only
		// way it can fail is if the index is out of range which is
		// impossible here.
		txHash, _ := block.TxSha(i)
		txInFlight[*txHash] = tx
	}

	// Loop through all of the transaction inputs (except for the coinbase
	// which has no inputs) collecting them into lists of what is needed and
	// what is already known (in-flight).
	var txNeededList []*btcwire.ShaHash
	txStore := make(map[btcwire.ShaHash]*txData)
	for _, tx := range block.MsgBlock().Transactions[1:] {
		for _, txIn := range tx.TxIn {
			// Add an entry to the transaction store for the needed
			// transaction with it set to missing by default.
			originHash := &txIn.PreviousOutpoint.Hash
			txD := &txData{hash: originHash, err: btcdb.TxShaMissing}
			txStore[*originHash] = txD

			// The transaction is already in-flight, so update the
			// transaction store acccordingly.  Otherwise, we need
			// it.
			if tx, ok := txInFlight[*originHash]; ok {
				txD.tx = tx
				txD.blockHeight = node.height
				txD.spent = make([]bool, len(tx.TxOut))
				txD.err = nil
			} else {
				txNeededList = append(txNeededList, originHash)
			}
		}
	}

	// Request the input transaction from the point of view of the node.
	txNeededStore, err := b.fetchTxList(node, txNeededList)
	if err != nil {
		return nil, err
	}

	// Merge the results of the requested transactions and the in-flight
	// transactions.
	for _, txD := range txNeededStore {
		txStore[*txD.hash] = txD
	}

	return txStore, nil
}
