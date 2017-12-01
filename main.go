package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"plugin"
	"strings"
	"sync"

	oidc "github.com/coreos/go-oidc"
	"github.com/go-accounting/fde"
	"github.com/julienschmidt/httprouter"
	yaml "gopkg.in/yaml.v2"
)

var storeSettings map[string]interface{}
var accountsRepositorySettings map[string]interface{}

var newStoreAndAccountsRepository func(map[string]interface{}, map[string]interface{}, *string, *string) (interface{}, interface{}, error)

var provider *oidc.Provider
var verifier *oidc.IDTokenVerifier

type repository struct {
	*fde.TxsRepository
	user  string
	coaid string
}

var repositoryPool = sync.Pool{
	New: func() interface{} {
		r := &repository{}
		v1, v2, err := newStoreAndAccountsRepository(storeSettings, accountsRepositorySettings, &r.user, &r.coaid)
		if err != nil {
			panic(err)
		}
		r.TxsRepository = fde.NewTxsRepository(v1.(fde.Store), v2.(fde.AccountsRepository))
		return r
	},
}

type decoder func(interface{}) error

func handler(
	f func(*repository, httprouter.Params, decoder) (interface{}, error),
) func(http.ResponseWriter, *http.Request, httprouter.Params) {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		user, err := user(r)
		if check(err, w) {
			return
		}
		cr := repositoryPool.Get().(*repository)
		cr.user = user
		cr.coaid = ps.ByName("coa")
		defer repositoryPool.Put(cr)
		v, err := f(cr, ps, func(v interface{}) error {
			return json.NewDecoder(r.Body).Decode(v)
		})
		if check(err, w) {
			return
		}
		if v != nil {
			w.Header().Set("Content-Type", "application/json")
			check(json.NewEncoder(w).Encode(v), w)
		}
	}
}

func check(err error, w http.ResponseWriter) bool {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return err != nil
}

func user(r *http.Request) (string, error) {
	var token string
	tokens, ok := r.Header["Authorization"]
	if ok && len(tokens) >= 1 {
		token = tokens[0]
		token = strings.TrimPrefix(token, "Bearer ")
	}
	idtoken, err := verifier.Verify(r.Context(), token)
	if err != nil {
		return "", err
	}
	var claims struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if err := idtoken.Claims(&claims); err != nil {
		return "", err
	}
	if !claims.Verified {
		return "", fmt.Errorf("email not verified")
	}
	return claims.Email, nil
}

func saveTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	var txs []*fde.Transaction
	if err := d(&txs); err != nil {
		return nil, err
	}
	return r.Save(txs...)
}

func deleteTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	return r.Delete(ps.ByName("txid"))
}

func loadSymbol(pluginFile, symbolName string) (interface{}, error) {
	p, err := plugin.Open(pluginFile)
	if err != nil {
		return nil, err
	}
	return p.Lookup(symbolName)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("usage: %v settings", path.Base(os.Args[0]))
		return
	}
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	var settings struct {
		Fde struct {
			PluginFile         string                 `yaml:"PluginFile"`
			Store              map[string]interface{} `yaml:"Store"`
			AccountsRepository map[string]interface{} `yaml:"AccountsRepository"`
		} `yaml:"Fde"`
		OpenId struct {
			Provider string `yaml:"Provider"`
			ClientId string `yaml:"ClientId"`
		} `yaml:"OpenId"`
	}
	err = yaml.Unmarshal(data, &settings)
	if err != nil {
		log.Fatal(err)
	}
	p, err := plugin.Open(settings.Fde.PluginFile)
	if err != nil {
		log.Fatal(err)
	}
	symbol, err := p.Lookup("NewStoreAndAccountsRepository")
	if err != nil {
		log.Fatal(err)
	}
	newStoreAndAccountsRepository = symbol.(func(map[string]interface{}, map[string]interface{}, *string, *string) (interface{}, interface{}, error))
	symbol, err = p.Lookup("LoadSymbolFunction")
	if err == nil {
		*symbol.(*func(string, string) (interface{}, error)) = loadSymbol
	}
	storeSettings = settings.Fde.Store
	accountsRepositorySettings = settings.Fde.AccountsRepository
	provider, err = oidc.NewProvider(context.Background(), settings.OpenId.Provider)
	if err != nil {
		log.Fatal(err)
	}
	verifier = provider.Verifier(&oidc.Config{ClientID: settings.OpenId.ClientId})
	router := httprouter.New()
	router.POST("/charts-of-accounts/:coa/transactions", handler(saveTransaction))
	router.PUT("/charts-of-accounts/:coa/transactions/:txid", handler(saveTransaction))
	router.DELETE("/charts-of-accounts/:coa/transactions/:txid", handler(deleteTransaction))
	log.Fatal(http.ListenAndServe(":8080", router))
}
