package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"sync"

	apisupport "github.com/go-accounting/api-support"
	"github.com/go-accounting/fde"
	"github.com/julienschmidt/httprouter"
)

var api *apisupport.Api

var storeSettings map[string]interface{}
var accountsRepositorySettings map[string]interface{}

var newStoreAndAccountsRepository func(map[string]interface{}, map[string]interface{}, *string, *string) (interface{}, interface{}, error)

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
		user, err := api.UserFromRequest(r)
		if api.Check(err, w) {
			return
		}
		cr := repositoryPool.Get().(*repository)
		cr.user = user
		cr.coaid = ps.ByName("coa")
		defer repositoryPool.Put(cr)
		v, err := f(cr, ps, func(v interface{}) error {
			return json.NewDecoder(r.Body).Decode(v)
		})
		if api.Check(err, w) {
			return
		}
		if v != nil {
			w.Header().Set("Content-Type", "application/json")
			api.Check(json.NewEncoder(w).Encode(v), w)
		}
	}
}

func saveTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	var txs []*fde.Transaction
	if err := d(&txs); err != nil {
		return nil, err
	}
	return r.Save(txs...)
}

func getTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	return r.Get(ps.ByName("txid"))
}

func updateTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	var tx *fde.Transaction
	if err := d(tx); err != nil {
		return nil, err
	}
	tx.Id = ps.ByName("txid")
	return r.Save(tx)
}

func deleteTransaction(r *repository, ps httprouter.Params, d decoder) (interface{}, error) {
	return r.Delete(ps.ByName("txid"))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("usage: %v settings", path.Base(os.Args[0]))
		return
	}
	api = apisupport.NewApi()
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
	err := api.UnmarshalSettings(os.Args[1], &settings)
	if err != nil {
		log.Fatal(err)
	}
	api.SetClientCredentials(settings.OpenId.Provider, settings.OpenId.ClientId)
	if api.Error() != nil {
		log.Fatal(api.Error())
	}
	symbol, err := api.LoadSymbol(settings.Fde.PluginFile, "NewStoreAndAccountsRepository")
	if err != nil {
		log.Fatal(err)
	}
	newStoreAndAccountsRepository = symbol.(func(map[string]interface{}, map[string]interface{}, *string, *string) (interface{}, interface{}, error))
	symbol, err = api.LoadSymbol(settings.Fde.PluginFile, "LoadSymbolFunction")
	if err == nil {
		*symbol.(*func(string, string) (interface{}, error)) = api.LoadSymbol
	}
	storeSettings = settings.Fde.Store
	accountsRepositorySettings = settings.Fde.AccountsRepository
	router := httprouter.New()
	router.POST("/charts-of-accounts/:coa/transactions", handler(saveTransaction))
	router.GET("/charts-of-accounts/:coa/transactions/:txid", handler(getTransaction))
	router.PUT("/charts-of-accounts/:coa/transactions/:txid", handler(updateTransaction))
	router.DELETE("/charts-of-accounts/:coa/transactions/:txid", handler(deleteTransaction))
	log.Fatal(http.ListenAndServe(":8080", router))
}
