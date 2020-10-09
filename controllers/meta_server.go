package controllers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const (
	ctxCrName    = "um-crName"
	ctxNamespace = "um-namespace"

	codeInvalidBasicAuth  = "INVALID_BASIC_AUTH"
	codeInstanceNotFound  = "UNDERMOON_INSTANCE_NOT_FOUND"
	codeIncorrectPassword = "INCORRECT_PASSWORD"
	codeVersionConflict   = "VERSION_CONFLICT"
)

var errExit = pkgerrors.New("exit error")

// This is defined by `undermoon` broker.
type externalStore struct {
	Version string                 `json:"version"`
	Store   map[string]interface{} `json:"store"`
}

type metaServer struct {
	metaCon   *metaController
	reqLogger logr.Logger
}

func newMetaServer(metaCon *metaController, reqLogger logr.Logger) *metaServer {
	return &metaServer{
		metaCon:   metaCon,
		reqLogger: reqLogger,
	}
}

func (server *metaServer) serve(ctx context.Context) error {
	r := gin.Default()
	v1 := r.Group("/api/v1", server.basicAuth)

	{
		v1.GET("/store/:storageName", server.handleGetMeta)
		v1.PUT("/store/:storageName", server.handleUpdateMeta)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", metaServicePort),
		Handler: r,
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return r.Run()
	})
	group.Go(func() error {
		<-ctx.Done()
		if err := srv.Shutdown(ctx); err != nil {
			log.Fatal("Server forced to shutdown:", err)
		}
		return errExit
	})

	return group.Wait()
}

func (server *metaServer) basicAuth(c *gin.Context) {
	storageName := c.Param("storageName")
	if len(storageName) == 0 {
		c.String(404, codeInstanceNotFound)
		return
	}

	auth := strings.SplitN(c.Request.Header.Get("Authorization"), " ", 2)

	if len(auth) != 2 || auth[0] != "Basic" {
		server.reqLogger.Info("invalid basic auth: can't find `Baisc`")
		c.String(401, codeInvalidBasicAuth)
		return
	}
	payload, err := base64.StdEncoding.DecodeString(auth[1])
	if err != nil {
		server.reqLogger.Info("invalid basic auth: base64 decode failed")
		c.String(401, codeInvalidBasicAuth)
		return
	}

	pair := strings.SplitN(string(payload), ":", 2)
	if len(pair) != 2 {
		server.reqLogger.Info("invalid basic auth: invalid username and password")
		c.String(401, codeInvalidBasicAuth)
		return
	}

	username := pair[0]
	password := pair[1]

	if storageName != username {
		server.reqLogger.Info("invalid basic auth: username not the same as storageName")
		c.String(401, codeInvalidBasicAuth)
		return
	}

	undermoonName, namespace, err := extractStorageName(username)
	if err != nil {
		server.reqLogger.Info("invalid basic auth: invalid username")
		c.String(401, codeInvalidBasicAuth)
		return
	}

	reqLogger := server.reqLogger.WithValues(
		"UndermoonName", undermoonName,
	)
	correct, err := server.metaCon.checkMetaSecret(reqLogger, undermoonName, namespace, password)
	if err != nil {
		c.String(401, codeInvalidBasicAuth)
		return
	}

	if !correct {
		c.String(401, codeIncorrectPassword)
		return
	}

	c.Set(ctxCrName, undermoonName)
	c.Set(ctxNamespace, namespace)

	c.Next()
}

func (server *metaServer) retrieveCtx(c *gin.Context) (string, string, bool) {
	crName, ok := c.Get(ctxCrName)
	if !ok {
		c.String(500, "cannot get username in context")
		return "", "", false
	}
	undermoonName, ok := crName.(string)
	if !ok {
		c.String(500, "invalid undermoonName")
		return "", "", false
	}
	namespace, ok := c.Get(ctxNamespace)
	if !ok {
		c.String(500, "cannot get username in context")
		return "", "", false
	}
	ns, ok := namespace.(string)
	if !ok {
		c.String(500, "invalid namespace")
		return "", "", false
	}

	return undermoonName, ns, true
}

func (server *metaServer) handleGetMeta(c *gin.Context) {
	undermoonName, namespace, ok := server.retrieveCtx(c)
	if !ok {
		return
	}

	reqLogger := server.reqLogger.WithValues(
		"UndermoonName", undermoonName,
		"Namespace", namespace,
	)
	store, err := server.metaCon.getExternalStore(reqLogger, undermoonName, namespace)
	if err != nil {
		c.String(500, fmt.Sprintf("%s", err))
		return
	}

	c.JSON(200, store)
}

func (server *metaServer) handleUpdateMeta(c *gin.Context) {
	undermoonName, namespace, ok := server.retrieveCtx(c)
	if !ok {
		return
	}

	reqLogger := server.reqLogger.WithValues(
		"UndermoonName", undermoonName,
		"Namespace", namespace,
	)

	store := externalStore{}
	err := c.ShouldBindJSON(&store)
	if err != nil {
		reqLogger.Error(err, "failed to get json from request")
		c.String(400, fmt.Sprintf("%s", err))
		return
	}

	err = server.metaCon.initOrUpdateExternalStore(reqLogger, undermoonName, namespace, &store)
	if err == errExternalStoreConflict {
		c.String(409, codeVersionConflict)
		return
	} else if err != nil {
		reqLogger.Error(err, "failed to update external store")
		c.String(400, fmt.Sprintf("%s", err))
		return
	}

	c.String(200, "")
}
