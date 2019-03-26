//Copyright (c) 2018, Oracle and/or its affiliates. All rights reserved.
//Licensed under the Universal Permissive License (UPL) Version 1.0 as shown at http://oss.oracle.com/licenses/upl.

package main

import (
	"fmt"
	"net"

	adsapi "gitlab-odx.oracledx.com/wcai/speedle/api/ads"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/assertion"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/cfg"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/cmd/flags"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/errors"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/eval"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/logging"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/store"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/svcs/adsgrpc"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/svcs/adsgrpc/pb"
	"gitlab-odx.oracledx.com/wcai/speedle/pkg/svcs/adsrest"

	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var gitCommit string
var productVersion string
var goVersion string

func printVersionInfo() {
	fmt.Printf("speedle-ads:\n")
	fmt.Printf(" Version:       %s\n", productVersion)
	fmt.Printf(" Go Version:    %s\n", goVersion)
	fmt.Printf(" Git commit:    %s\n", gitCommit)
}

func main() {

	storeParamsMap := store.GetAllStoreParams()

	var params flags.Parameters
	params.ParseFlags(flags.DefaultAuthzCheckEndPoint, printVersionInfo, storeParamsMap)
	params.ValidateFlags()

	conf, _ := params.Param2Config(storeParamsMap)

	// Initialize the logging
	if conf.LogConfig != nil {
		err := logging.InitLog(conf.LogConfig)
		if err != nil {
			log.Errorf("Authz_check failed to initialize the log module, err: %v.", err)
		}
	} else {
		log.Error("No any log configurations for authorization service.")
	}

	// Initialize the Audit logging
	if conf.AuditLogConfig != nil {
		err := logging.InitAuditLog(conf.AuditLogConfig)
		if err != nil {
			log.Errorf("Authz_check failed to initialize the audit log module, err: %v.", err)
		}
	} else {
		log.Error("No any audit log configurations for authorization service.\n")
	}

	evaluator, err := newEvaluator(conf)
	if err != nil {
		log.Panic(err)
		return
	}

	grpcServer, err := newGRPCServer(evaluator)
	if err != nil {
		log.Panic(err)
	}
	log.Info("Starting the gRPC server for authorization service...")
	go listenGRPCServer(grpcServer)

	log.Info("Starting the REST server for authorization service...")
	panic(listenAndServe(&params, evaluator))
}

func newEvaluator(conf *cfg.Config) (eval.InternalEvaluator, error) {
	evaluator, err := eval.NewFromConfig(conf)
	if err != nil {
		return nil, err
	}

	log.Info("Loading asserters.")
	as, errLoadAsserter := assertion.NewAsserter(conf.AsserterWebhookConfig, nil)
	if errLoadAsserter != nil {
		log.Warningf("load asserter error: %v", errLoadAsserter)
	} else {
		f := func(ctx *adsapi.RequestContext) error {
			if ctx.Subject != nil &&
				len(ctx.Subject.TokenType) != 0 &&
				len(ctx.Subject.Token) != 0 {
				tokenType := ctx.Subject.TokenType
				token := ctx.Subject.Token
				log.Debugf("Asserting token %s with token type %s.", token, tokenType)
				s, err := as.AssertToken(token, tokenType, "", nil)
				if err == nil {
					for _, p := range s.Principals {
						ctx.Subject.Principals = append(ctx.Subject.Principals, p)
					}
				} else {
					log.Errorf("Failed to assert due to error %v.", err)
					return err
				}
			}
			return nil
		}
		evaluator.SetAsserterFunc(f)
	}

	return evaluator, nil
}

func newGRPCServer(evaluator eval.InternalEvaluator) (*grpc.Server, error) {

	serviceImpl, err := adsgrpc.NewGRPCService(evaluator)
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer()
	pb.RegisterEvaluatorServer(server, serviceImpl)
	// Register reflection service on gRPC server.
	reflection.Register(server)
	return server, nil
}

func listenGRPCServer(server *grpc.Server) error {
	lis, err := net.Listen("tcp", ":50002")
	if err != nil {
		return errors.Wrap(err, errors.ServerError, "failed to listen on endpoint :50002")
	}
	if err := server.Serve(lis); err != nil {
		return errors.Wrap(err, errors.ServerError, "failed to serve for endpoint :50002")
	}
	return nil
}

func listenAndServe(params *flags.Parameters, evaluator eval.InternalEvaluator) error {
	routers, err := adsrest.NewRouter(evaluator)
	if err != nil {
		return err
	}
	return params.ListenAndServe(routers)
}