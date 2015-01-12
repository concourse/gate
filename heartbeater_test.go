package gate_test

import (
	"encoding/json"
	"net/http"
	"time"

	garden "github.com/cloudfoundry-incubator/garden/api"
	gfakes "github.com/cloudfoundry-incubator/garden/api/fakes"
	"github.com/concourse/atc"
	. "github.com/concourse/gate"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/rata"
)

var _ = Describe("Heartbeater", func() {
	type registration struct {
		worker atc.Worker
		ttl    time.Duration
	}

	var (
		logger lager.Logger

		addrToRegister string
		interval       time.Duration

		fakeGardenClient *gfakes.FakeClient
		fakeATC          *ghttp.Server

		heartbeater ifrit.Process

		registrations <-chan registration
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test")

		addrToRegister = "1.2.3.4:7777"
		interval = time.Second

		fakeATC = ghttp.NewServer()

		registerRoute, found := atc.Routes.FindRouteByName(atc.RegisterWorker)
		Ω(found).Should(BeTrue())

		registered := make(chan registration, 100)
		registrations = registered

		fakeATC.RouteToHandler(registerRoute.Method, registerRoute.Path, func(w http.ResponseWriter, r *http.Request) {
			var worker atc.Worker
			err := json.NewDecoder(r.Body).Decode(&worker)
			Ω(err).ShouldNot(HaveOccurred())

			ttl, err := time.ParseDuration(r.URL.Query().Get("ttl"))
			Ω(err).ShouldNot(HaveOccurred())

			registered <- registration{worker, ttl}
		})

		fakeGardenClient = new(gfakes.FakeClient)
	})

	JustBeforeEach(func() {
		atcEndpoint := rata.NewRequestGenerator(fakeATC.URL(), atc.Routes)
		heartbeater = ifrit.Invoke(NewHeartbeater(logger, addrToRegister, interval, fakeGardenClient, atcEndpoint))
	})

	AfterEach(func() {
		ginkgomon.Interrupt(heartbeater)
	})

	Context("when Garden returns containers", func() {
		var returnedContainers chan<- []garden.Container

		BeforeEach(func() {
			containers := make(chan []garden.Container, 2)
			returnedContainers = containers

			containers <- []garden.Container{
				new(gfakes.FakeContainer),
				new(gfakes.FakeContainer),
			}

			containers <- []garden.Container{
				new(gfakes.FakeContainer),
				new(gfakes.FakeContainer),
				new(gfakes.FakeContainer),
				new(gfakes.FakeContainer),
				new(gfakes.FakeContainer),
			}

			close(containers)

			fakeGardenClient.ContainersStub = func(garden.Properties) ([]garden.Container, error) {
				return <-containers, nil
			}
		})

		It("immediately registers", func() {
			Ω(registrations).Should(Receive(Equal(registration{
				worker: atc.Worker{Addr: addrToRegister, ActiveContainers: 2},
				ttl:    2 * interval,
			})))
		})

		Context("when the interval passes after the initial registration", func() {
			JustBeforeEach(func() {
				Ω(registrations).Should(Receive(Equal(registration{
					worker: atc.Worker{Addr: addrToRegister, ActiveContainers: 2},
					ttl:    2 * interval,
				})))

				time.Sleep(interval)
			})

			It("heartbeats", func() {
				Eventually(registrations).Should(Receive(Equal(registration{
					worker: atc.Worker{Addr: addrToRegister, ActiveContainers: 5},
					ttl:    2 * interval,
				})))
			})
		})
	})
})
