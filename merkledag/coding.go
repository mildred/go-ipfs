package merkledag

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"sort"

	mh "github.com/ipfs/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multihash"

	pb "github.com/ipfs/go-ipfs/merkledag/pb"
	u "github.com/ipfs/go-ipfs/util"
	ipld "github.com/ipfs/go-ipld"
	ipldcoding "github.com/ipfs/go-ipld/coding"
)

// for now, we use a PBNode intermediate thing.
// because native go objects are nice.

// Marshal encodes a *Node instance into a new byte slice.
// The conversion uses an intermediate PBNode.
func (n *Node) Marshal() ([]byte, error) {
	pbn := n.getPBNode()
	data, err := pbn.Marshal()
	if err != nil {
		return data, fmt.Errorf("Marshal failed. %v", err)
	}
	return data, nil
}

func (n *Node) getPBNode() *pb.PBNode {
	pbn := &pb.PBNode{}
	if len(n.Links) > 0 {
		pbn.Links = make([]*pb.PBLink, len(n.Links))
	}

	sort.Stable(LinkSlice(n.Links)) // keep links sorted
	for i, l := range n.Links {
		pbn.Links[i] = &pb.PBLink{}
		pbn.Links[i].Name = &l.Name
		pbn.Links[i].Tsize = &l.Size
		pbn.Links[i].Hash = []byte(l.Hash)
	}

	if len(n.Data) > 0 {
		pbn.Data = n.Data
	}
	return pbn
}

// Encoded returns the encoded raw data version of a Node instance.
// It may use a cached encoded version, unless the force flag is given.
func (n *Node) Encoded(force bool) ([]byte, error) {
	sort.Stable(LinkSlice(n.Links)) // keep links sorted
	if n.encoded == nil || force {
		var err error
		n.encoded, err = n.Marshal()
		if err != nil {
			return nil, err
		}
		n.cached = u.Hash(n.encoded)
	}

	return n.encoded, nil
}

// Decoded decodes raw data and returns a new Node instance.
func DecodeProtobuf(encoded []byte) (*Node, error) {
	r, err := ipldcoding.DecodeLegacyProtobufBytes(encoded)
	if err != nil {
		return nil, err
	}

	n := new(Node)
	var links []Link

	it := ipld.Iterate(r, nil)
	defer it.Close()
	for it.Iter() {
		if it.TokenType == ipld.TokenNode {
			path := item.StringPath()
			name := ""
			if len(path) > 0 {
				name = path[len(path)-1]
			}
			links = append(links, Link{name, 0, nil, nil})

		} else if it.TokenType == ipld.TokenKey && it.Value == ipld.LinkKey {
			it.Iter()
			h, err := ipld.ReadLinkPath(it.Value)
			if err == nil {
				links[len(links)-1].Hash = h
			}

		} else if it.TokenType == ipld.TokenKey && it.Value == "size" {
			it.Iter()
			if s, ok := it.ToUint(); ok {
				links[len(links)-1].Size = s
			}

		} else if it.Token == ipld.TokenEndNode {
			lastLink = &links[len(links)-1]
			if lastLink.Hash != nil && lastLink.Name != "" {
				Node.Links = append(Node.Links, lastLink)
			}
			links = links[:len(links)-1]
		}
		return nil
	}

	return n, it.LastError
}
