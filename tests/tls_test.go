package tests

import (
	"context"
	"flag"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/workflowservice/v1"
	sdkclient "go.temporal.io/sdk/client"
	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/common/rpc"
)

type tlsIntegrationSuite struct {
	IntegrationBase
	hostPort  string
	sdkClient sdkclient.Client
}

func TestTLSIntegrationSuite(t *testing.T) {
	flag.Parse()
	suite.Run(t, new(tlsIntegrationSuite))
}

func (s *tlsIntegrationSuite) SetupSuite() {
	s.setupSuite("testdata/tls_integration_test_cluster.yaml")
	s.hostPort = "127.0.0.1:7134"
	if TestFlags.FrontendAddr != "" {
		s.hostPort = TestFlags.FrontendAddr
	}
}

func (s *tlsIntegrationSuite) TearDownSuite() {
	s.tearDownSuite()
}

func (s *tlsIntegrationSuite) SetupTest() {
	var err error
	s.sdkClient, err = sdkclient.Dial(sdkclient.Options{
		HostPort:  s.hostPort,
		Namespace: s.namespace,
		ConnectionOptions: sdkclient.ConnectionOptions{
			TLS: s.testCluster.host.tlsConfigProvider.FrontendClientConfig,
		},
	})
	if err != nil {
		s.Logger.Fatal("Error when creating SDK client", tag.Error(err))
	}
}

func (s *tlsIntegrationSuite) TearDownTest() {
	s.sdkClient.Close()
}

func (s *tlsIntegrationSuite) TestGRPCMTLS() {
	ctx, cancel := rpc.NewContextWithTimeoutAndVersionHeaders(time.Minute)
	defer cancel()

	// Track auth info
	calls := s.trackAuthInfoByCall()

	// Make a list-open call
	s.sdkClient.ListOpenWorkflow(ctx, &workflowservice.ListOpenWorkflowExecutionsRequest{})

	// Confirm auth info as expected
	authInfo, ok := calls.Load("/temporal.api.workflowservice.v1.WorkflowService/ListOpenWorkflowExecutions")
	s.Require().True(ok)
	s.Require().Equal(tlsCertCommonName, authInfo.(*authorization.AuthInfo).TLSSubject.CommonName)
}

func (s *tlsIntegrationSuite) TestHTTPMTLS() {
	// Track auth info
	calls := s.trackAuthInfoByCall()

	// Confirm non-HTTPS call is rejected with 400
	resp, err := http.Get("http://" + s.httpAPIAddress + "/api/v1/namespaces/" + s.namespace + "/workflows")
	s.Require().NoError(err)
	s.Require().Equal(http.StatusBadRequest, resp.StatusCode)

	// Create HTTP client with TLS config
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: s.testCluster.host.tlsConfigProvider.FrontendClientConfig,
		},
	}

	// Make a list call
	req, err := http.NewRequest("GET", "https://"+s.httpAPIAddress+"/api/v1/namespaces/"+s.namespace+"/workflows", nil)
	s.Require().NoError(err)
	resp, err = httpClient.Do(req)
	s.Require().NoError(err)
	s.Require().Equal(http.StatusOK, resp.StatusCode)

	// Confirm auth info as expected
	authInfo, ok := calls.Load("/temporal.api.workflowservice.v1.WorkflowService/ListWorkflowExecutions")
	s.Require().True(ok)
	s.Require().Equal(tlsCertCommonName, authInfo.(*authorization.AuthInfo).TLSSubject.CommonName)
}

func (s *tlsIntegrationSuite) trackAuthInfoByCall() *sync.Map {
	var calls sync.Map
	// Put auth info on claim, then use authorizer to set on the map by call
	s.testCluster.host.onGetClaims = func(authInfo *authorization.AuthInfo) (*authorization.Claims, error) {
		return &authorization.Claims{
			System:     authorization.RoleAdmin,
			Extensions: authInfo,
		}, nil
	}
	s.testCluster.host.onAuthorize = func(
		ctx context.Context,
		caller *authorization.Claims,
		target *authorization.CallTarget,
	) (authorization.Result, error) {
		if authInfo, _ := caller.Extensions.(*authorization.AuthInfo); authInfo != nil {
			calls.Store(target.APIName, authInfo)
		}
		return authorization.Result{Decision: authorization.DecisionAllow}, nil
	}
	return &calls
}
