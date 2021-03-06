// Copyright (c) 2013 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcchain_test

import (
	"compress/bzip2"
	"encoding/binary"
	"github.com/conformal/btcchain"
	"github.com/conformal/btcdb"
	_ "github.com/conformal/btcdb/sqlite3"
	"github.com/conformal/btcutil"
	"github.com/conformal/btcwire"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReorganization loads a set of test blocks which force a chain
// reorganization to test the block chain handling code.
// The test blocks were originally from a post on the bitcoin talk forums:
// https://bitcointalk.org/index.php?topic=46370.msg577556#msg577556
func TestReorganization(t *testing.T) {
	// Intentionally load the side chain blocks out of order to ensure
	// orphans are handled properly along with chain reorganization.
	testFiles := [...]string{
		"blk_0_to_4.dat.bz2",
		"blk_4A.dat.bz2",
		"blk_5A.dat.bz2",
		"blk_3A.dat.bz2",
	}

	var blocks []*btcutil.Block
	for _, file := range testFiles {
		blockTmp, err := loadBlocks(file)
		if err != nil {
			t.Errorf("Error loading file: %v\n", err)
		}
		for _, block := range blockTmp {
			blocks = append(blocks, block)
		}
	}

	t.Logf("Number of blocks: %v\n", len(blocks))

	dbname := "chaintest"
	_ = os.Remove(dbname)
	db, err := btcdb.CreateDB("sqlite", dbname)
	if err != nil {
		t.Errorf("Error creating db: %v\n", err)
	}
	// Clean up
	defer os.Remove(dbname)
	defer db.Close()

	// Since we're not dealing with the real block chain, disable
	// checkpoints and set the coinbase maturity to 1.
	blockChain := btcchain.New(db, btcwire.MainNet, nil)
	blockChain.DisableCheckpoints(true)
	btcchain.TstSetCoinbaseMaturity(1)

	for i := 1; i < len(blocks); i++ {
		err = blockChain.ProcessBlock(blocks[i])
		if err != nil {
			t.Errorf("ProcessBlock fail on block %v: %v\n", i, err)
			return
		}
	}
	db.Sync()

	return
}

// loadBlocks reads files containing bitcoin block data (gzipped but otherwise
// in the format bitcoind writes) from disk and returns them as an array of
// btcutil.Block.  This is largely borrowed from the test code in btcdb.
func loadBlocks(filename string) (blocks []*btcutil.Block, err error) {
	filename = filepath.Join("testdata/", filename)

	var network = btcwire.MainNet
	var dr io.Reader
	var fi io.ReadCloser

	fi, err = os.Open(filename)
	if err != nil {
		return
	}

	if strings.HasSuffix(filename, ".bz2") {
		dr = bzip2.NewReader(fi)
	} else {
		dr = fi
	}
	defer fi.Close()

	var block *btcutil.Block

	err = nil
	for height := int64(1); err == nil; height++ {
		var rintbuf uint32
		err = binary.Read(dr, binary.LittleEndian, &rintbuf)
		if err == io.EOF {
			// hit end of file at expected offset: no warning
			height--
			err = nil
			break
		}
		if err != nil {
			break
		}
		if rintbuf != uint32(network) {
			break
		}
		err = binary.Read(dr, binary.LittleEndian, &rintbuf)
		blocklen := rintbuf

		rbytes := make([]byte, blocklen)

		// read block
		dr.Read(rbytes)

		block, err = btcutil.NewBlockFromBytes(rbytes, btcwire.ProtocolVersion)
		if err != nil {
			return
		}
		blocks = append(blocks, block)
	}

	return
}
