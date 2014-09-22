package dht

import (
	"bytes"
	"encoding/json"

	context "github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"

	peer "github.com/jbenet/go-ipfs/peer"
	kb "github.com/jbenet/go-ipfs/routing/kbucket"
	u "github.com/jbenet/go-ipfs/util"
)

// This file implements the Routing interface for the IpfsDHT struct.

// Basic Put/Get

// PutValue adds value corresponding to given Key.
// This is the top level "Store" operation of the DHT
func (dht *IpfsDHT) PutValue(ctx context.Context, key u.Key, value []byte) error {
	peers := []*peer.Peer{}

	// get the peers we need to announce to
	for _, route := range dht.routingTables {
		npeers := route.NearestPeers(kb.ConvertKey(key), KValue)
		peers = append(peers, npeers...)
	}

	query := newQuery(key, func(ctx context.Context, p *peer.Peer) (*dhtQueryResult, error) {
		u.DOut("[%s] PutValue qry part %v\n", dht.self.ID.Pretty(), p.ID.Pretty())
		err := dht.putValueToNetwork(ctx, p, string(key), value)
		if err != nil {
			return nil, err
		}
		return &dhtQueryResult{success: true}, nil
	})

	_, err := query.Run(ctx, peers)
	u.DOut("[%s] PutValue %v %v\n", dht.self.ID.Pretty(), key, value)
	return err
}

// GetValue searches for the value corresponding to given Key.
// If the search does not succeed, a multiaddr string of a closer peer is
// returned along with util.ErrSearchIncomplete
func (dht *IpfsDHT) GetValue(ctx context.Context, key u.Key) ([]byte, error) {
	ll := startNewRPC("GET")
	defer ll.EndAndPrint()

	// If we have it local, dont bother doing an RPC!
	// NOTE: this might not be what we want to do...
	val, err := dht.getLocal(key)
	if err == nil {
		ll.Success = true
		return val, nil
	}

	// get closest peers in the routing tables
	routeLevel := 0
	closest := dht.routingTables[routeLevel].NearestPeers(kb.ConvertKey(key), PoolSize)
	if closest == nil || len(closest) == 0 {
		return nil, kb.ErrLookupFailure
	}

	// setup the Query
	query := newQuery(key, func(ctx context.Context, p *peer.Peer) (*dhtQueryResult, error) {

		val, peers, err := dht.getValueOrPeers(ctx, p, key, routeLevel)
		if err != nil {
			return nil, err
		}

		res := &dhtQueryResult{value: val, closerPeers: peers}
		if val != nil {
			res.success = true
		}

		return res, nil
	})

	// run it!
	result, err := query.Run(ctx, closest)
	if err != nil {
		return nil, err
	}

	u.DOut("[%s] GetValue %v %v\n", dht.self.ID.Pretty(), key, result.value)
	if result.value == nil {
		return nil, u.ErrNotFound
	}

	return result.value, nil
}

// Value provider layer of indirection.
// This is what DSHTs (Coral and MainlineDHT) do to store large values in a DHT.

// Provide makes this node announce that it can provide a value for the given key
func (dht *IpfsDHT) Provide(ctx context.Context, key u.Key) error {

	dht.providers.AddProvider(key, dht.self)
	peers := dht.routingTables[0].NearestPeers(kb.ConvertKey(key), PoolSize)
	if len(peers) == 0 {
		// Early out for no targets
		return nil
	}

	//TODO FIX: this doesn't work! it needs to be sent to the actual nearest peers.
	// `peers` are the closest peers we have, not the ones that should get the value.
	for _, p := range peers {
		err := dht.putProvider(ctx, p, string(key))
		if err != nil {
			return err
		}
	}
	return nil
}

// NB: not actually async. Used to keep the interface consistent while the
// actual async method, FindProvidersAsync2 is under construction
func (dht *IpfsDHT) FindProvidersAsync(ctx context.Context, key u.Key, count int) <-chan *peer.Peer {
	ch := make(chan *peer.Peer)
	providers, err := dht.FindProviders(ctx, key)
	if err != nil {
		close(ch)
		return ch
	}
	go func() {
		defer close(ch)
		for _, p := range providers {
			ch <- p
		}
	}()
	return ch
}

// FIXME: there's a bug here!
func (dht *IpfsDHT) FindProvidersAsync2(ctx context.Context, key u.Key, count int) <-chan *peer.Peer {
	peerOut := make(chan *peer.Peer, count)
	go func() {
		ps := newPeerSet()
		provs := dht.providers.GetProviders(key)
		for _, p := range provs {
			count--
			// NOTE: assuming that this list of peers is unique
			ps.Add(p)
			peerOut <- p
			if count <= 0 {
				return
			}
		}

		peers := dht.routingTables[0].NearestPeers(kb.ConvertKey(key), AlphaValue)
		for _, pp := range peers {
			ppp := pp
			go func() {
				pmes, err := dht.findProvidersSingle(ctx, ppp, key, 0)
				if err != nil {
					u.PErr("%v\n", err)
					return
				}
				dht.addPeerListAsync(key, pmes.GetProviderPeers(), ps, count, peerOut)
			}()
		}

	}()
	return peerOut
}

//TODO: this function could also be done asynchronously
func (dht *IpfsDHT) addPeerListAsync(k u.Key, peers []*Message_Peer, ps *peerSet, count int, out chan *peer.Peer) {
	for _, pbp := range peers {

		// construct new peer
		p, err := dht.ensureConnectedToPeer(pbp)
		if err != nil {
			continue
		}

		dht.providers.AddProvider(k, p)
		if ps.AddIfSmallerThan(p, count) {
			out <- p
		} else if ps.Size() >= count {
			return
		}
	}
}

// FindProviders searches for peers who can provide the value for given key.
func (dht *IpfsDHT) FindProviders(ctx context.Context, key u.Key) ([]*peer.Peer, error) {
	ll := startNewRPC("FindProviders")
	ll.EndAndPrint()

	// get closest peer
	u.DOut("Find providers for: '%s'\n", key)
	p := dht.routingTables[0].NearestPeer(kb.ConvertKey(key))
	if p == nil {
		return nil, kb.ErrLookupFailure
	}

	for level := 0; level < len(dht.routingTables); {

		// attempt retrieving providers
		pmes, err := dht.findProvidersSingle(ctx, p, key, level)
		if err != nil {
			return nil, err
		}

		// handle providers
		provs := pmes.GetProviderPeers()
		if provs != nil {
			u.DOut("Got providers back from findProviders call!\n")
			return dht.addProviders(key, provs), nil
		}

		u.DOut("Didnt get providers, just closer peers.\n")
		closer := pmes.GetCloserPeers()
		if len(closer) == 0 {
			level++
			continue
		}

		np, err := dht.peerFromInfo(closer[0])
		if err != nil {
			u.DOut("no peerFromInfo")
			level++
			continue
		}
		p = np
	}
	return nil, u.ErrNotFound
}

// Find specific Peer

// FindPeer searches for a peer with given ID.
func (dht *IpfsDHT) FindPeer(ctx context.Context, id peer.ID) (*peer.Peer, error) {

	// Check if were already connected to them
	p, _ := dht.Find(id)
	if p != nil {
		return p, nil
	}

	// @whyrusleeping why is this here? doesn't the dht.Find above cover it?
	routeLevel := 0
	p = dht.routingTables[routeLevel].NearestPeer(kb.ConvertPeerID(id))
	if p == nil {
		return nil, kb.ErrLookupFailure
	}
	if p.ID.Equal(id) {
		return p, nil
	}

	for routeLevel < len(dht.routingTables) {
		pmes, err := dht.findPeerSingle(ctx, p, id, routeLevel)
		plist := pmes.GetCloserPeers()
		if plist == nil || len(plist) == 0 {
			routeLevel++
			continue
		}
		found := plist[0]

		nxtPeer, err := dht.ensureConnectedToPeer(found)
		if err != nil {
			routeLevel++
			continue
		}

		if nxtPeer.ID.Equal(id) {
			return nxtPeer, nil
		}

		p = nxtPeer
	}
	return nil, u.ErrNotFound
}

func (dht *IpfsDHT) findPeerMultiple(ctx context.Context, id peer.ID) (*peer.Peer, error) {

	// Check if were already connected to them
	p, _ := dht.Find(id)
	if p != nil {
		return p, nil
	}

	// get the peers we need to announce to
	routeLevel := 0
	peers := dht.routingTables[routeLevel].NearestPeers(kb.ConvertPeerID(id), AlphaValue)
	if len(peers) == 0 {
		return nil, kb.ErrLookupFailure
	}

	// setup query function
	query := newQuery(u.Key(id), func(ctx context.Context, p *peer.Peer) (*dhtQueryResult, error) {
		pmes, err := dht.findPeerSingle(ctx, p, id, routeLevel)
		if err != nil {
			u.DErr("getPeer error: %v\n", err)
			return nil, err
		}

		plist := pmes.GetCloserPeers()
		if len(plist) == 0 {
			routeLevel++
		}

		nxtprs := make([]*peer.Peer, len(plist))
		for i, fp := range plist {
			nxtp, err := dht.peerFromInfo(fp)
			if err != nil {
				u.DErr("findPeer error: %v\n", err)
				continue
			}

			if nxtp.ID.Equal(id) {
				return &dhtQueryResult{peer: nxtp, success: true}, nil
			}

			nxtprs[i] = nxtp
		}

		return &dhtQueryResult{closerPeers: nxtprs}, nil
	})

	result, err := query.Run(ctx, peers)
	if err != nil {
		return nil, err
	}

	if result.peer == nil {
		return nil, u.ErrNotFound
	}
	return result.peer, nil
}

// Ping a peer, log the time it took
func (dht *IpfsDHT) Ping(ctx context.Context, p *peer.Peer) error {
	// Thoughts: maybe this should accept an ID and do a peer lookup?
	u.DOut("Enter Ping.\n")

	pmes := newMessage(Message_PING, "", 0)
	_, err := dht.sendRequest(ctx, p, pmes)
	return err
}

func (dht *IpfsDHT) getDiagnostic(ctx context.Context) ([]*diagInfo, error) {

	u.DOut("Begin Diagnostic")
	peers := dht.routingTables[0].NearestPeers(kb.ConvertPeerID(dht.self.ID), 10)
	var out []*diagInfo

	query := newQuery(dht.self.Key(), func(ctx context.Context, p *peer.Peer) (*dhtQueryResult, error) {
		pmes := newMessage(Message_DIAGNOSTIC, "", 0)
		rpmes, err := dht.sendRequest(ctx, p, pmes)
		if err != nil {
			return nil, err
		}

		dec := json.NewDecoder(bytes.NewBuffer(rpmes.GetValue()))
		for {
			di := new(diagInfo)
			err := dec.Decode(di)
			if err != nil {
				break
			}

			out = append(out, di)
		}
		return &dhtQueryResult{success: true}, nil
	})

	_, err := query.Run(ctx, peers)
	return out, err
}
