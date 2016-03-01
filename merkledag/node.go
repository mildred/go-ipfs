package merkledag

import (
	"fmt"

	"gx/ipfs/QmZy2y8t9zQH2a1b8q2ZSLKp17ATuJoCNxxyMFG5qFExpt/go-net/context"

	key "github.com/ipfs/go-ipfs/blocks/key"
	ipldlinks "github.com/ipfs/go-ipld/links"
	ipldstream "github.com/ipfs/go-ipld/stream"
	mh "gx/ipfs/QmYf7ng2hG5XBtJA3tN34DQ2GUN5HNksEw1rLDkmr6vGku/go-multihash"
)

var ErrLinkNotFound = fmt.Errorf("no link by that name")

type IPLDNode interface {
	ipldstream.NodeReader
	Multihash() (mh.Multihash, error)
	Key() (key.Key, error)
	Size() (uint64, error)
}

type ipldNode struct {
	ipldstream.NodeReader
	hash mh.Multihash
	size uint64
}

func (n *ipldNode) Key() (key.Key, error) {
	return key.Key(n.hash), nil
}

func (n *ipldNode) Multihash() (mh.Multihash, error) {
	return n.hash, nil
}

func (n *ipldNode) Size() (uint64, error) {
	return n.size, nil
}

// Node represents a node in the IPFS Merkle DAG.
// nodes have opaque data and a set of navigable links.
type Node struct {
	Links []*Link
	Data  []byte

	// cache encoded/marshaled value
	encoded []byte

	cached mh.Multihash
}

// NodeStat is a statistics object for a Node. Mostly sizes.
type NodeStat struct {
	Hash           string
	NumLinks       int // number of links in link table
	BlockSize      int // size of the raw, encoded data
	LinksSize      int // size of the links segment
	DataSize       int // size of the data segment
	CumulativeSize int // cumulative size of object and its references
}

func (ns NodeStat) String() string {
	f := "NodeStat{NumLinks: %d, BlockSize: %d, LinksSize: %d, DataSize: %d, CumulativeSize: %d}"
	return fmt.Sprintf(f, ns.NumLinks, ns.BlockSize, ns.LinksSize, ns.DataSize, ns.CumulativeSize)
}

// Link represents an IPFS Merkle DAG Link between Nodes.
type Link struct {
	// utf string name. should be unique per object
	Name string // utf8

	// cumulative size of target object
	Size uint64

	// multihash of the target object
	Hash mh.Multihash
}

type LinkSlice []*Link

func (ls LinkSlice) Len() int           { return len(ls) }
func (ls LinkSlice) Swap(a, b int)      { ls[a], ls[b] = ls[b], ls[a] }
func (ls LinkSlice) Less(a, b int) bool { return ls[a].Name < ls[b].Name }

// MakeLink creates a link to the given node
func MakeLink(n *Node) (*Link, error) {
	s, err := n.Size()
	if err != nil {
		return nil, err
	}

	h, err := n.Multihash()
	if err != nil {
		return nil, err
	}
	return &Link{
		Size: s,
		Hash: h,
	}, nil
}

// GetNode returns the MDAG Node that this link points to
func (l *Link) GetNode(ctx context.Context, serv DAGService) (*Node, error) {
	return serv.GetPB(ctx, key.Key(l.Hash))
}

// AddNodeLink adds a link to another node.
func (n *Node) AddNodeLink(name string, that *Node) error {
	n.encoded = nil

	lnk, err := MakeLink(that)

	lnk.Name = name
	if err != nil {
		return err
	}

	n.AddRawLink(name, lnk)

	return nil
}

// AddNodeLinkClean adds a link to another node. without keeping a reference to
// the child node
func (n *Node) AddNodeLinkClean(name string, that *Node) error {
	n.encoded = nil
	lnk, err := MakeLink(that)
	if err != nil {
		return err
	}
	n.AddRawLink(name, lnk)

	return nil
}

// AddRawLink adds a copy of a link to this node
func (n *Node) AddRawLink(name string, l *Link) error {
	n.encoded = nil
	n.Links = append(n.Links, &Link{
		Name: name,
		Size: l.Size,
		Hash: l.Hash,
	})

	return nil
}

// Remove a link on this node by the given name
func (n *Node) RemoveNodeLink(name string) error {
	n.encoded = nil
	good := make([]*Link, 0, len(n.Links))
	var found bool

	for _, l := range n.Links {
		if l.Name != name {
			good = append(good, l)
		} else {
			found = true
		}
	}
	n.Links = good

	if !found {
		return ErrNotFound
	}

	return nil
}

// Return a copy of the link with given name
func (n *Node) GetNodeLink(name string) (*Link, error) {
	for _, l := range n.Links {
		if l.Name == name {
			return &Link{
				Name: l.Name,
				Size: l.Size,
				Hash: l.Hash,
			}, nil
		}
	}
	return nil, ErrLinkNotFound
}

func (n *Node) GetLinkedNode(ctx context.Context, ds DAGService, name string) (*Node, error) {
	lnk, err := n.GetNodeLink(name)
	if err != nil {
		return nil, err
	}

	return lnk.GetNode(ctx, ds)
}

// Copy returns a copy of the node.
// NOTE: does not make copies of Node objects in the links.
func (n *Node) Copy() *Node {
	nnode := new(Node)
	if len(n.Data) > 0 {
		nnode.Data = make([]byte, len(n.Data))
		copy(nnode.Data, n.Data)
	}

	if len(n.Links) > 0 {
		nnode.Links = make([]*Link, len(n.Links))
		copy(nnode.Links, n.Links)
	}
	return nnode
}

// UpdateNodeLink return a copy of the node with the link name set to point to
// that. If a link of the same name existed, it is removed.
func (n *Node) UpdateNodeLink(name string, that *Node) (*Node, error) {
	newnode := n.Copy()
	err := newnode.RemoveNodeLink(name)
	err = nil // ignore error
	err = newnode.AddNodeLink(name, that)
	return newnode, err
}

// Size returns the total size of the data addressed by node,
// including the total sizes of references.
func (n *Node) Size() (uint64, error) {
	b, err := n.EncodeProtobuf(false)
	if err != nil {
		return 0, err
	}

	s := uint64(len(b))
	for _, l := range n.Links {
		s += l.Size
	}
	return s, nil
}

// Stat returns statistics on the node.
func (n *Node) Stat() (*NodeStat, error) {
	enc, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}

	cumSize, err := n.Size()
	if err != nil {
		return nil, err
	}

	key, err := n.Key()
	if err != nil {
		return nil, err
	}

	return &NodeStat{
		Hash:           key.B58String(),
		NumLinks:       len(n.Links),
		BlockSize:      len(enc),
		LinksSize:      len(enc) - len(n.Data), // includes framing.
		DataSize:       len(n.Data),
		CumulativeSize: int(cumSize),
	}, nil
}

// Multihash hashes the encoded data of this node.
func (n *Node) Multihash() (mh.Multihash, error) {
	// Note: EncodeProtobuf generates the hash and puts it in n.cached.
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}

	return n.cached, nil
}

// Key returns the Multihash as a key, for maps.
func (n *Node) Key() (key.Key, error) {
	h, err := n.Multihash()
	return key.Key(h), err
}

func (n *Node) Read(fun ipldstream.ReadFun) error {
	err := readNode(n, fun)
	if err == ipldstream.NodeReadAbort || err == ipldstream.NodeReadSkip {
		err = nil
	}
	return err
}

func readNode(n *Node, fun ipldstream.ReadFun) error {
	err := fun([]interface{}{}, ipldstream.TokenNode, nil)
	if err != nil {
		return err
	}

	err = fun([]interface{}{}, ipldstream.TokenKey, "data")
	if err != nil && err != ipldstream.NodeReadSkip {
		return err
	} else if err != ipldstream.NodeReadSkip {

		err = fun([]interface{}{"data"}, ipldstream.TokenValue, n.Data)
		if err != nil && err != ipldstream.NodeReadSkip {
			return err
		}

	}

	err = fun([]interface{}{}, ipldstream.TokenKey, "links")
	if err != nil && err != ipldstream.NodeReadSkip {
		return err
	} else if err != ipldstream.NodeReadSkip {

		err = fun([]interface{}{"links"}, ipldstream.TokenArray, nil)
		if err != nil && err != ipldstream.NodeReadSkip {
			return err
		} else if err != ipldstream.NodeReadSkip {

			for i, lnk := range n.Links {

				err = fun([]interface{}{"links"}, ipldstream.TokenIndex, i)
				if err == ipldstream.NodeReadSkip {
					continue
				} else if err != nil {
					return err
				}

				err = fun([]interface{}{"links", i}, ipldstream.TokenNode, nil)
				if err == ipldstream.NodeReadSkip {
					continue
				} else if err != nil {
					return err
				}

				err = fun([]interface{}{"links", i}, ipldstream.TokenKey, ipldlinks.LinkKey)
				if err != nil && err != ipldstream.NodeReadSkip {
					return err
				} else if err != ipldstream.NodeReadSkip {

					err = fun([]interface{}{"links", i, ipldlinks.LinkKey}, ipldstream.TokenValue, lnk.Hash.B58String())
					if err != nil && err != ipldstream.NodeReadSkip {
						return err
					}

				}

				err = fun([]interface{}{"links", i}, ipldstream.TokenKey, "name")
				if err != nil && err != ipldstream.NodeReadSkip {
					return err
				} else if err != ipldstream.NodeReadSkip {

					err = fun([]interface{}{"links", i, "name"}, ipldstream.TokenValue, lnk.Name)
					if err != nil && err != ipldstream.NodeReadSkip {
						return err
					}

				}

				err = fun([]interface{}{"links", i}, ipldstream.TokenKey, "size")
				if err != nil && err != ipldstream.NodeReadSkip {
					return err
				} else if err != ipldstream.NodeReadSkip {

					err = fun([]interface{}{"links", i, "size"}, ipldstream.TokenValue, uint64(lnk.Size))
					if err != nil && err != ipldstream.NodeReadSkip {
						return err
					}

				}

				err = fun([]interface{}{"links", i}, ipldstream.TokenEndNode, nil)
				if err != nil && err != ipldstream.NodeReadSkip {
					return err
				}

			}

			err = fun([]interface{}{"links"}, ipldstream.TokenEndArray, nil)
			if err != nil && err != ipldstream.NodeReadSkip {
				return err
			}

		}

	}

	return fun([]interface{}{}, ipldstream.TokenEndNode, nil)
}
