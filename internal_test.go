// Copyright (c) 2013 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

/*
This test file is part of the btcchain package rather than than the
btcchain_test package so it can bridge access to the internals to properly test
cases which are either not possible or can't reliably be tested via the public
interface.  The functions are only exported while the tests are being run.
*/

package btcchain

import (
	"github.com/conformal/btcutil"
	"time"
)

// TstCheckBlockSanity makes the internal checkBlockSanity function available to
// the test package.
func TstCheckBlockSanity(block *btcutil.Block) error {
	return checkBlockSanity(block)
}

// TstSetCoinbaseMaturity makes the ability to set the coinbase maturity
// available to the test package.
func TstSetCoinbaseMaturity(maturity int64) {
	coinbaseMaturity = maturity
}

// TstTimeSorter makes the internal timeSorter type available to the test
// package.
func TstTimeSorter(times []time.Time) timeSorter {
	return timeSorter(times)
}
