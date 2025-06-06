package descriptors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testdataDir           = "testdata"
	derivationTestVectors = "derivation.json"
)

func parseNetwork(t *testing.T, network string) Network {
	switch network {
	case "mainnet":
		return NetworkMainnet
	case "testnet":
		return NetworkTestnet
	case "regtest":
		return NetworkRegtest
	default:
		require.FailNow(t, "unknown network: %s", network)
		return 0
	}
}

// parseMiniscript returns the miniscript internal types and number of opcodes
// in the miniscript.
func parseMiniscript(script string) (string, uint64, error) {
	mod := getWasmMod()
	jsonRes, err := mod.miniscriptParse(script)
	if err != nil {
		return "", 0, err
	}

	return jsonRes.Types, jsonRes.OpCodes, nil
}

// compileMiniscript compiles the given miniscript to a Bitcoin script. The
// miniscript expression must be sane, meaning all public key placeholders need
// to be replaced with actual public keys.
func compileMiniscript(script string) ([]byte, error) {
	mod := getWasmMod()
	return mod.miniscriptCompile(script)
}

func TestDerivationVectors(t *testing.T) {
	vectors, err := readDerivationTestVectors()
	require.NoError(t, err)

	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			descriptor, err := NewDescriptor(vector.Descriptor)

			if vector.ExpectErr != "" {
				require.ErrorContains(t, err, vector.ExpectErr)
				return
			}

			require.NoError(t, err)
			defer descriptor.Close()

			require.EqualValues(
				t, vector.NumMultipath,
				descriptor.MultipathLen(),
			)

			expectedDescriptor := vector.Descriptor
			if !vector.HasChecksum {
				expectedDescriptor += vector.Checksum
			}
			require.Equal(
				t, expectedDescriptor, descriptor.String(),
			)

			for _, addr := range vector.Addresses {
				network := parseNetwork(t, addr.Network)

				parsedAddr, err := descriptor.AddressAt(
					network, addr.MultipathIndex,
					addr.DerivationIndex,
				)

				if addr.ExpectErr != "" {
					require.ErrorContains(
						t, err, addr.ExpectErr,
					)
					continue
				}

				require.NoError(t, err)
				require.Equal(t, addr.Address, parsedAddr)
			}
		})
	}
}

type testAddress struct {
	Network         string
	MultipathIndex  uint32
	DerivationIndex uint32
	Address         string
	ExpectErr       string
}

type derivationTestVector struct {
	Name         string
	Descriptor   string
	HasChecksum  bool
	Checksum     string
	NumMultipath uint32
	ExpectErr    string
	Addresses    []*testAddress
}

// readDerivationTestVectors reads the derivation test vectors from the test
// vector file.
func readDerivationTestVectors() ([]*derivationTestVector, error) {
	// Open the test vector file.
	file, err := os.Open(filepath.Join(testdataDir, derivationTestVectors))
	if err != nil {
		return nil, err
	}

	// Decode the test vectors.
	var testVectors []*derivationTestVector
	if err := json.NewDecoder(file).Decode(&testVectors); err != nil {
		return nil, err
	}

	return testVectors, nil
}

// checkMiniscript makes sure the passed miniscript is top level, has the
// expected type and script length.
func checkMiniscript(miniscript, expectedType string, opCodes int,
	scriptHex string) error {

	parsedTypes, parsedOpCodes, err := parseMiniscript(miniscript)
	if err != nil {
		return err
	}

	sortString := func(s string) string {
		r := []rune(s)
		sort.Slice(r, func(i, j int) bool {
			return r[i] < r[j]
		})
		return string(r)
	}

	if sortString(expectedType) != sortString(parsedTypes) {
		return fmt.Errorf("expected type %s, got %s",
			sortString(expectedType), sortString(parsedTypes))
	}

	if opCodes != 0 {
		if opCodes != int(parsedOpCodes) {
			return fmt.Errorf("expected op codes %d, got %d",
				opCodes, parsedOpCodes)
		}
	}

	if scriptHex == "" {
		return nil
	}

	script, err := compileMiniscript(miniscript)
	if err != nil {
		return fmt.Errorf("failed to compile miniscript: %v", err)
	}

	if scriptHex != fmt.Sprintf("%x", script) {
		return fmt.Errorf("expected script %s, got %x", scriptHex,
			script)
	}

	return nil
}

// TestVectors asserts all test vectors in the test data text files pass.
func TestVectors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		fileName    string
		valid       bool
		withScript  bool
		withOpCodes bool
	}{
		{
			// Invalid expressions (failed type check).
			fileName: "testdata/invalid_from_alloy.txt",
			valid:    false,
		},
		{
			// Valid miniscript expressions including the expected
			// type.
			fileName: "testdata/valid_8f1e8_from_alloy.txt",
			valid:    true,
		},
		{
			// Valid miniscript expressions including the expected
			// type.
			fileName: "testdata/valid_from_alloy.txt",
			valid:    true,
		},
		{
			// Valid expressions but do not contain the `m` type
			// property, i.e. the script is guaranteed to have a
			// non-malleable satisfaction.
			fileName: "testdata/malleable_from_alloy.txt",
			valid:    true,
		},
		{
			// miniscripts with time lock mixing in `after` (same
			// expression contains both time-based and block-based
			// time locks). This unit test is not testing this
			// currently, see
			// https://github.com/rust-bitcoin/rust-miniscript/issues/514.
			fileName: "testdata/conflict_from_alloy.txt",
			valid:    true,
		},
		{
			// miniscripts with number of opcodes.
			fileName:    "testdata/opcodes.txt",
			valid:       true,
			withOpCodes: true,
		},
		{
			// miniscripts with compilable expressions.
			fileName:    "testdata/compile.txt",
			valid:       true,
			withScript:  true,
			withOpCodes: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.fileName, func(t *testing.T) {
			content, err := os.ReadFile(tc.fileName)
			require.NoError(t, err)

			lines := strings.Split(string(content), "\n")
			for i, line := range lines {
				if line == "" {
					continue
				}

				if !tc.valid {
					_, _, err := parseMiniscript(line)
					require.Errorf(t, err, "failure on "+
						"line %d: %s", i, line)

					continue
				}

				parts := strings.Split(line, " ")

				var (
					opCodes int
					script  string
				)
				switch {
				case tc.withOpCodes && tc.withScript:
					require.Lenf(t, parts, 4, "malformed "+
						"test on line %d: %s", i, line)
					opCodes, err = strconv.Atoi(parts[2])
					require.NoError(t, err)
					script = parts[3]

				case tc.withOpCodes:
					require.Lenf(t, parts, 3, "malformed "+
						"test on line %d: %s", i, line)
					opCodes, err = strconv.Atoi(parts[2])
					require.NoError(t, err)

				case tc.withScript:
					t.Fatalf("script without opcodes not " +
						"supported")

				default:
					require.Lenf(t, parts, 2, "malformed "+
						"test on line %d: %s", i, line)
				}

				miniscript, expectedType := parts[0], parts[1]
				err = checkMiniscript(
					miniscript, expectedType, opCodes,
					script,
				)
				require.NoErrorf(t, err, "failure on line %d: "+
					"%s", i, line)
			}
		})
	}
}
