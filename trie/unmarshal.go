package dageth_trie

import (
	"fmt"
	"io"
	"io/ioutil"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/multiformats/go-multihash"
)

// DecodeTrieNode provides an IPLD codec decode interface for merkle patricia trie nodes
// It's not possible to meet the Decode(na ipld.NodeAssembler, in io.Reader) interface
// for a function that supports all trie types (multicodec types), unlike with encoding
// this is used by Decode functions for each trie type, which are the ones registered to their
// corresponding multicodec
func DecodeTrieNode(na ipld.NodeAssembler, in io.Reader, codec uint64) error {
	var src []byte
	if buf, ok := in.(interface{ Bytes() []byte }); ok {
		src = buf.Bytes()
	} else {
		var err error
		src, err = ioutil.ReadAll(in)
		if err != nil {
			return err
		}
	}
	return DecodeTrieNodeBytes(na, src, codec)
}

// DecodeTrieNodeBytes is like DecodeTrieNode, but it uses an input buffer directly.
func DecodeTrieNodeBytes(na ipld.NodeAssembler, src []byte, codec uint64) error {
	var nodeFields []interface{}
	if err := rlp.DecodeBytes(src, &nodeFields); err != nil {
		return err
	}
	ma, err := na.BeginMap(1)
	if err != nil {
		return err
	}
	switch len(nodeFields) {
	case 2:
		nodeKind, decoded, err := decodeCompactKey(nodeFields)
		if err != nil {
			return err
		}
		switch nodeKind {
		case EXTENSION_NODE:
			if err := ma.AssembleKey().AssignString(EXTENSION_NODE.String()); err != nil {
				return err
			}
			extNodeMA, err := ma.AssembleValue().BeginMap(2)
			if err != nil {
				return err
			}
			if err := unpackExtensionNode(extNodeMA, decoded, codec); err != nil {
				return err
			}
			if err := extNodeMA.Finish(); err != nil {
				return err
			}
		case LEAF_NODE:
			if err := ma.AssembleKey().AssignString(LEAF_NODE.String()); err != nil {
				return err
			}
			leafNodeMA, err := ma.AssembleValue().BeginMap(2)
			if err != nil {
				return err
			}
			if err := unpackLeafNode(leafNodeMA, decoded, codec); err != nil {
				return err
			}
			if err := leafNodeMA.Finish(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unrecognized trie node type %s", nodeKind.String())
		}
	case 17:
		if err := ma.AssembleKey().AssignString(BRANCH_NODE.String()); err != nil {
			return err
		}
		branchNodeMA, err := ma.AssembleValue().BeginMap(17)
		if err != nil {
			return err
		}
		if err := unpackBranchNode(branchNodeMA, nodeFields, codec); err != nil {
			return err
		}
		if err := branchNodeMA.Finish(); err != nil {
			return err
		}
	}
	return ma.Finish()
}

func unpackExtensionNode(ma ipld.MapAssembler, nodeFields []interface{}, codec uint64) error {
	partialPath, ok := nodeFields[0].([]byte)
	if !ok {
		return fmt.Errorf("extension node requires partial path byte slice")
	}
	if err := ma.AssembleKey().AssignString("PartialPath"); err != nil {
		return err
	}
	if err := ma.AssembleValue().AssignBytes(partialPath); err != nil { // de-compact partial path first?
		return err
	}
	if err := ma.AssembleKey().AssignString("Child"); err != nil {
		return err
	}
	childLink, ok := nodeFields[1].([]byte)
	if ok {
		// it's a hash referencing the child node
		// make CID link from the bytes
		// assign the link value to the MA
		childCID := keccak256ToCid(codec, childLink)
		childCIDLink := cidlink.Link{Cid: childCID}
		return ma.AssembleValue().AssignLink(childCIDLink)
	}
	// the child node is included directly
	// it must be a leaf node, branch and extension will never be less than 32 bytes
	childLeaf, ok := nodeFields[1].([]interface{})
	if !ok {
		return fmt.Errorf("unable to decode extension node element into []byte or []interface{}")
	}
	if len(childLeaf) != 2 {
		return fmt.Errorf("unexpected number of entries for leaf node; got %d want 2", len(childLeaf))
	}
	childNode, err := ma.AssembleValue().BeginMap(1)
	if err != nil {
		return err
	}
	if err := childNode.AssembleKey().AssignString("Leaf"); err != nil {
		return err
	}
	leafNodeMA, err := childNode.AssembleValue().BeginMap(2)
	if err != nil {
		return err
	}
	if err := unpackLeafNode(leafNodeMA, childLeaf, codec); err != nil {
		return err
	}
	if err := leafNodeMA.Finish(); err != nil {
		return err
	}
	return childNode.Finish()
}

func unpackBranchNode(ma ipld.MapAssembler, nodeFields []interface{}, codec uint64) error {
	for i := 0; i < 16; i++ {
		key := fmt.Sprintf("Child%d", i)
		if err := ma.AssembleKey().AssignString(key); err != nil {
			return err
		}
		childLink, ok := nodeFields[i].([]byte)
		if ok {
			switch len(childLink) {
			case 0:
				if err := ma.AssembleValue().AssignNull(); err != nil {
					return err
				}
			case 32:
				childCID := keccak256ToCid(codec, childLink)
				childCIDLink := cidlink.Link{Cid: childCID}
				if err := ma.AssembleValue().AssignLink(childCIDLink); err != nil {
					return err
				}
			default:
				return fmt.Errorf("branch node child of unexpected length %d", len(childLink))
			}
			continue
		}
		childLeaf, ok := nodeFields[i].([]interface{})
		if !ok {
			return fmt.Errorf("unable to decode branch node entry into []byte or []interface{}")
		}
		if len(childLeaf) != 2 {
			return fmt.Errorf("unexpected number of entries for leaf node; got %d want 2", len(childLeaf))
		}
		childNode, err := ma.AssembleValue().BeginMap(1)
		if err != nil {
			return err
		}
		if err := childNode.AssembleKey().AssignString("Leaf"); err != nil {
			return err
		}
		leafNodeMA, err := childNode.AssembleValue().BeginMap(2)
		if err != nil {
			return err
		}
		if err := unpackLeafNode(leafNodeMA, childLeaf, codec); err != nil {
			return err
		}
		if err := leafNodeMA.Finish(); err != nil {
			return err
		}
		if err := childNode.Finish(); err != nil {
			return err
		}
	}
	if err := ma.AssembleKey().AssignString("Value"); err != nil {
		return err
	}
	valBytes, ok := nodeFields[16].([]byte)
	if !ok {
		return fmt.Errorf("branch node 17th member should be a byte array (val)")
	}
	if len(valBytes) == 0 {
		return ma.AssembleValue().AssignNull()
	}
	return ma.AssembleValue().AssignBytes(valBytes)
}

func unpackLeafNode(ma ipld.MapAssembler, nodeFields []interface{}, codec uint64) error {
	partialPath, ok := nodeFields[0].([]byte)
	if !ok {
		return fmt.Errorf("leaf node requires partial path byte slice")
	}
	valBytes, ok := nodeFields[1].([]byte)
	if !ok {
		return fmt.Errorf("leaf node requires value byte slice")
	}
	if err := ma.AssembleKey().AssignString("PartialPath"); err != nil {
		return err
	}
	if err := ma.AssembleValue().AssignBytes(partialPath); err != nil { // de-compact partial path first???
		return err
	}
	if err := ma.AssembleKey().AssignString("Value"); err != nil {
		return err
	}
	return ma.AssembleValue().AssignBytes(valBytes)
}

// decodeCompactKey takes a compact key, and returns its nodeKind and value.
func decodeCompactKey(i []interface{}) (NodeKind, []interface{}, error) {
	first := i[0].([]byte)

	switch first[0] / 16 {
	case '\x00':
		return EXTENSION_NODE, []interface{}{
			nibbleToByte(first)[2:],
			i[1],
		}, nil
	case '\x01':
		return EXTENSION_NODE, []interface{}{
			nibbleToByte(first)[1:],
			i[1],
		}, nil
	case '\x02':
		return LEAF_NODE, []interface{}{
			nibbleToByte(first)[2:],
			i[1],
		}, nil
	case '\x03':
		return LEAF_NODE, []interface{}{
			nibbleToByte(first)[1:],
			i[1],
		}, nil
	default:
		return UNKNOWN_NODE, nil, fmt.Errorf("unknown hex prefix")
	}
}

// keccak256ToCid takes a keccak256 hash and returns its cid based on
// the codec given.
func keccak256ToCid(codec uint64, h []byte) cid.Cid {
	buf, err := multihash.Encode(h, multihash.KECCAK_256)
	if err != nil {
		panic(err)
	}

	return cid.NewCidV1(codec, multihash.Multihash(buf))
}

// nibbleToByte expands the nibbles of a byte slice into their own bytes.
func nibbleToByte(k []byte) []byte {
	var out []byte

	for _, b := range k {
		out = append(out, b/16)
		out = append(out, b%16)
	}

	return out
}
