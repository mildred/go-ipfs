package corehttp

import (
	b64 "encoding/base64"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"strings"

	"github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"
	mh "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multihash"

	core "github.com/jbenet/go-ipfs/core"
	"github.com/jbenet/go-ipfs/importer"
	chunk "github.com/jbenet/go-ipfs/importer/chunk"
	dag "github.com/jbenet/go-ipfs/merkledag"
	crypto "github.com/jbenet/go-ipfs/p2p/crypto"
	p "github.com/jbenet/go-ipfs/path"
	"github.com/jbenet/go-ipfs/routing"
	ufs "github.com/jbenet/go-ipfs/unixfs"
	uio "github.com/jbenet/go-ipfs/unixfs/io"
	u "github.com/jbenet/go-ipfs/util"
)

type gateway interface {
	ResolvePath(string) (*dag.Node, error)
	NewDagFromReader(io.Reader) (*dag.Node, error)
	AddNodeToDAG(nd *dag.Node) (u.Key, error)
	NewDagReader(nd *dag.Node) (io.Reader, error)
}

// shortcut for templating
type webHandler map[string]interface{}

// struct for directory listing
type directoryItem struct {
	Size uint64
	Name string
}

// gatewayHandler is a HTTP handler that serves IPFS objects (accessible by default at /ipfs/<path>)
// (it serves requests like GET /ipfs/QmVRzPKPzNtSrEzBFm2UZfxmPAgnaLke4DMcerbsGGSaFe/link)
type gatewayHandler struct {
	node     *core.IpfsNode
	dirList  *template.Template
	writable bool
}

func newGatewayHandler(node *core.IpfsNode, writable bool) (*gatewayHandler, error) {
	i := &gatewayHandler{
		node:     node,
		writable: writable,
	}
	err := i.loadTemplate()
	if err != nil {
		return nil, err
	}
	return i, nil
}

// Load the directroy list template
func (i *gatewayHandler) loadTemplate() error {
	t, err := template.New("dir").Parse(listingTemplate)
	if err != nil {
		return err
	}
	i.dirList = t
	return nil
}

func (i *gatewayHandler) resolveNamePath(path string) (string, error) {
	if path[0:5] == "/ipns" {
		ipns_record, _, pathext := u.Partition(path[6:], "/")

		value, err := i.node.Namesys.Resolve(ipns_record)
		if err != nil {
			return "", err
		}

		path = "/ipfs/" + value + "/" + pathext
	}
	return path, nil
}

func (i *gatewayHandler) ResolvePath(path string) (*dag.Node, error) {
	path, err := i.resolveNamePath(path)
	if err != nil {
		return nil, err
	}
	return i.node.Resolver.ResolvePath(path)
}

func (i *gatewayHandler) NewDagFromReader(r io.Reader) (*dag.Node, error) {
	return importer.BuildDagFromReader(
		r, i.node.DAG, i.node.Pinning.GetManual(), chunk.DefaultSplitter)
}

func NewDagEmptyDir() *dag.Node {
	return &dag.Node{Data: ufs.FolderPBData()}
}

func (i *gatewayHandler) AddNodeToDAG(nd *dag.Node) (u.Key, error) {
	return i.node.DAG.Add(nd)
}

func (i *gatewayHandler) NewDagReader(nd *dag.Node) (io.Reader, error) {
	return uio.NewDagReader(nd, i.node.DAG)
}

func (i *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if i.writable && r.Method == "POST" {
		i.postHandler(w, r)
		return
	}

	if i.writable && r.Method == "PUT" {
		i.putHandler(w, r, r.URL.Path)
		return
	}

	if i.writable && r.Method == "DELETE" {
		i.deleteHandler(w, r, r.URL.Path)
		return
	}

	if r.Method == "GET" {
		i.getHandler(w, r, r.URL.Path)
		return
	}

	errmsg := "Method " + r.Method + " not allowed: "
	if !i.writable {
		w.WriteHeader(http.StatusMethodNotAllowed)
		errmsg = errmsg + "read only access"
	} else {
		w.WriteHeader(http.StatusBadRequest)
		errmsg = errmsg + "bad request for " + r.URL.Path
	}
	w.Write([]byte(errmsg))
	log.Error(errmsg)
}

func (i *gatewayHandler) getHandler(w http.ResponseWriter, r *http.Request, path string) {
	nd, err := i.ResolvePath(path)
	if err != nil {
		if err == routing.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else if err == context.DeadlineExceeded {
			w.WriteHeader(http.StatusRequestTimeout)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}

		log.Error(err)
		w.Write([]byte(err.Error()))
		return
	}

	extensionIndex := strings.LastIndex(path, ".")
	if extensionIndex != -1 {
		extension := path[extensionIndex:]
		mimeType := mime.TypeByExtension(extension)
		if len(mimeType) > 0 {
			w.Header().Add("Content-Type", mimeType)
		}
	}

	dr, err := i.NewDagReader(nd)
	if err == nil {
		io.Copy(w, dr)
		return
	}

	if err != uio.ErrIsDir {
		// not a directory and still an error
		internalWebError(w, err)
		return
	}

	log.Debug("listing directory")
	if path[len(path)-1:] != "/" {
		log.Debug("missing trailing slash, redirect")
		http.Redirect(w, r, path+"/", 307)
		return
	}

	// storage for directory listing
	var dirListing []directoryItem
	// loop through files
	foundIndex := false
	for _, link := range nd.Links {
		if link.Name == "index.html" {
			log.Debug("found index")
			foundIndex = true
			// return index page instead.
			nd, err := i.ResolvePath(path + "/index.html")
			if err != nil {
				internalWebError(w, err)
				return
			}
			dr, err := i.NewDagReader(nd)
			if err != nil {
				internalWebError(w, err)
				return
			}
			// write to request
			io.Copy(w, dr)
			break
		}

		dirListing = append(dirListing, directoryItem{link.Size, link.Name})
	}

	if !foundIndex {
		// template and return directory listing
		hndlr := webHandler{"listing": dirListing, "path": path}
		if err := i.dirList.Execute(w, hndlr); err != nil {
			internalWebError(w, err)
			return
		}
	}
}

func (i *gatewayHandler) postHandler(w http.ResponseWriter, r *http.Request) {

	if strings.ToLower(r.Header.Get("IPNS")) == "update" {
		i.postNameHandler(w, r)
		return
	}

	nd, err := i.NewDagFromReader(r.Body)
	if err != nil {
		internalWebError(w, err)
		return
	}

	k, err := i.AddNodeToDAG(nd)
	if err != nil {
		internalWebError(w, err)
		return
	}

	h := mh.Multihash(k).B58String()
	w.Header().Set("IPFS-Hash", h)
	http.Redirect(w, r, "/ipfs/"+h, http.StatusCreated)
}

func (i *gatewayHandler) postNameHandler(w http.ResponseWriter, r *http.Request) {
	keydata, err := b64.StdEncoding.DecodeString(r.Header.Get("IPNS-PublicKey"))
	if err != nil {
		webError(w, "Could not decode header IPNS-PublicKey", err, http.StatusBadRequest)
		return
	}

	var pubkey crypto.PubKey
	key_type := strings.ToLower(r.Header.Get("IPNS-PublicKey-Type"))
	if key_type == "rsa" {
		pubkey, err = crypto.UnmarshalRsaPublicKey(keydata)
		if err != nil {
			webError(w, "Could not decode RSA Public Key", err, http.StatusBadRequest)
			return
		}
	} else if key_type == "ed25519" {
		pubkey, err = crypto.UnmarshalEd25519PublicKey(keydata)
		if err != nil {
			webError(w, "Could not decode Ed25519 Public Key", err, http.StatusBadRequest)
			return
		}
	} else {
		webError(w, "Could not recognize key type "+key_type, err, http.StatusBadRequest)
		return
	}

	pkbytes, err := pubkey.Bytes()
	if err != nil {
		webError(w, "Could not read key bytes", err, http.StatusInternalServerError)
		return
	}
	nameb := u.Hash(pkbytes)

	data, err := ioutil.ReadAll(r.Body)
	i.node.Namesys.PublishEntry(pubkey, data)

	w.Header().Set("IPNS-Hash", nameb.B58String())
	http.Redirect(w, r, "/ipns/"+nameb.B58String(), http.StatusCreated)
}

func (i *gatewayHandler) putEmptyDirHandler(w http.ResponseWriter, r *http.Request) {
	newnode := NewDagEmptyDir()

	key, err := i.node.DAG.Add(newnode)
	if err != nil {
		webError(w, "Could not recursively add new node", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("IPFS-Hash", key.String())
	http.Redirect(w, r, "/ipfs/"+key.String()+"/", http.StatusCreated)
}

func (i *gatewayHandler) putHandler(w http.ResponseWriter, r *http.Request, path string) {
	pathext := path[5:]
	var err error
	if path == "/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn/" {
		i.putEmptyDirHandler(w, r)
		return
	}

	var newnode *dag.Node
	if pathext[len(pathext)-1] == '/' {
		newnode = NewDagEmptyDir()
	} else {
		newnode, err = i.NewDagFromReader(r.Body)
		if err != nil {
			webError(w, "Could not create DAG from request", err, http.StatusInternalServerError)
			return
		}
	}

	ipfspath, err := i.resolveNamePath(path)
	if err != nil {
		// FIXME HTTP error code
		webError(w, "Could not resolve name", err, http.StatusInternalServerError)
		return
	}

	h, components, err := p.SplitAbsPath(ipfspath)
	if err != nil {
		webError(w, "Could not split path", err, http.StatusInternalServerError)
		return
	}

	if len(components) < 1 {
		err = fmt.Errorf("Cannot override existing object")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		log.Error("%s", err)
		return
	}

	rootnd, err := i.node.Resolver.DAG.Get(u.Key(h))
	if err != nil {
		webError(w, "Could not resolve root object", err, http.StatusBadRequest)
		return
	}

	// resolving path components into merkledag nodes. if a component does not
	// resolve, create empty directories (which will be linked and populated below.)
	path_nodes, err := i.node.Resolver.ResolveLinks(rootnd, components[:len(components)-1])
	if _, ok := err.(p.ErrNoLink); ok {
		// Create empty directories, links will be made further down the code
		for len(path_nodes) < len(components) {
			path_nodes = append(path_nodes, NewDagEmptyDir())
		}
	} else if err != nil {
		webError(w, "Could not resolve parent object", err, http.StatusBadRequest)
		return
	}

	for i := len(path_nodes) - 1; i >= 0; i-- {
		newnode, err = path_nodes[i].UpdateNodeLink(components[i], newnode)
		if err != nil {
			webError(w, "Could not update node links", err, http.StatusInternalServerError)
			return
		}
	}

	err = i.node.DAG.AddRecursive(newnode)
	if err != nil {
		webError(w, "Could not add recursively new node", err, http.StatusInternalServerError)
		return
	}

	// Redirect to new path
	key, err := newnode.Key()
	if err != nil {
		webError(w, "Could not get key of new node", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("IPFS-Hash", key.String())
	http.Redirect(w, r, "/ipfs/"+key.String()+"/"+strings.Join(components, "/"), http.StatusCreated)
}

func (i *gatewayHandler) deleteHandler(w http.ResponseWriter, r *http.Request, path string) {
	ipfspath, err := i.resolveNamePath(path)
	if err != nil {
		// FIXME HTTP error code
		webError(w, "Could not resolve name", err, http.StatusInternalServerError)
		return
	}

	h, components, err := p.SplitAbsPath(ipfspath)
	if err != nil {
		webError(w, "Could not split path", err, http.StatusInternalServerError)
		return
	}

	rootnd, err := i.node.Resolver.DAG.Get(u.Key(h))
	if err != nil {
		webError(w, "Could not resolve root object", err, http.StatusBadRequest)
		return
	}

	path_nodes, err := i.node.Resolver.ResolveLinks(rootnd, components[:len(components)-1])
	if err != nil {
		webError(w, "Could not resolve parent object", err, http.StatusBadRequest)
		return
	}

	err = path_nodes[len(path_nodes)-1].RemoveNodeLink(components[len(components)-1])
	if err != nil {
		webError(w, "Could not delete link", err, http.StatusBadRequest)
		return
	}

	newnode := path_nodes[len(path_nodes)-1]
	for i := len(path_nodes) - 2; i >= 0; i-- {
		newnode, err = path_nodes[i].UpdateNodeLink(components[i], newnode)
		if err != nil {
			webError(w, "Could not update node links", err, http.StatusInternalServerError)
			return
		}
	}

	err = i.node.DAG.AddRecursive(newnode)
	if err != nil {
		webError(w, "Could not add recursively new node", err, http.StatusInternalServerError)
		return
	}

	// Redirect to new path
	key, err := newnode.Key()
	if err != nil {
		webError(w, "Could not get key of new node", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("IPFS-Hash", key.String())
	http.Redirect(w, r, "/ipfs/"+key.String()+"/"+strings.Join(components[:len(components)-1], "/"), http.StatusCreated)
}

func webError(w http.ResponseWriter, message string, err error, defaultCode int) {
	if _, ok := err.(p.ErrNoLink); ok {
		webErrorWithCode(w, message, err, http.StatusNotFound)
	} else if err == routing.ErrNotFound {
		webErrorWithCode(w, message, err, http.StatusNotFound)
	} else if err == context.DeadlineExceeded {
		webErrorWithCode(w, message, err, http.StatusRequestTimeout)
	} else {
		webErrorWithCode(w, message, err, defaultCode)
	}
}

func webErrorWithCode(w http.ResponseWriter, message string, err error, code int) {
	w.WriteHeader(code)
	log.Errorf("%s: %s", message, err)
	w.Write([]byte(message + ": " + err.Error()))
}

// return a 500 error and log
func internalWebError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(err.Error()))
	log.Error("%s", err)
}

// Directory listing template
var listingTemplate = `
<!DOCTYPE html>
<html>
	<head>
		<meta charset="utf-8" />
		<title>{{ .path }}</title>
	</head>
	<body>
	<h2>Index of {{ .path }}</h2>
	<ul>
	<li><a href="./..">..</a></li>
  {{ range $item := .listing }}
	<li><a href="./{{ $item.Name }}">{{ $item.Name }}</a> - {{ $item.Size }} bytes</li>
	{{ end }}
	</ul>
	</body>
</html>
`
