#!/bin/sh
#
# Copyright (c) 2014 Christian Couder
# MIT Licensed; see the LICENSE file in this repository.
#

test_description="Test installation and some basic commands"

. lib/test-lib.sh

test_expect_success "current dir is writable" '
	echo "It works!" >test.txt
'

test_expect_success "ipfs version succeeds" '
	ipfs version >version.txt
'

test_expect_success "ipfs version output looks good" '
	egrep "^ipfs version [0-9]+\.[0-9]+\.[0-9]" version.txt >/dev/null ||
	test_fsh cat version.txt
'

test_expect_success "ipfs help succeeds" '
	ipfs help >help.txt
'

test_expect_success "ipfs help output looks good" '
	egrep -i "^Usage:" help.txt >/dev/null &&
	egrep "ipfs .* <command>" help.txt >/dev/null ||
	test_fsh cat help.txt
'

test_expect_success "'ipfs commands' succeeds" '
	ipfs commands >commands.txt
'

test_expect_success "'ipfs commands' output looks good" '
	grep "ipfs add" commands.txt &&
	grep "ipfs daemon" commands.txt &&
	grep "ipfs update" commands.txt
'

test_expect_success "All commands accept --help" '
	while read -r cmd
	do
		echo "running: $cmd --help"
		$cmd --help </dev/null >/dev/null || return
	done <commands.txt
'

test_done
