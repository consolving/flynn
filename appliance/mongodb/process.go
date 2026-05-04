package mongodb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	mongodbxlog "github.com/flynn/flynn/appliance/mongodb/xlog"
	"github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/pkg/shutdown"
	"github.com/flynn/flynn/pkg/sirenia/client"
	"github.com/flynn/flynn/pkg/sirenia/state"
	"github.com/flynn/flynn/pkg/sirenia/xlog"
	"github.com/inconshreveable/log15"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

const (
	DefaultHost        = "127.0.0.1"
	DefaultPort        = "27017"
	DefaultBinDir      = "/usr/bin"
	DefaultDataDir     = "/data"
	DefaultPassword    = ""
	DefaultOpTimeout   = 5 * time.Minute
	DefaultReplTimeout = 1 * time.Minute

	BinName    = "mongod"
	ConfigName = "mongod.conf"

	checkInterval = 1000 * time.Millisecond
)

var (
	// ErrRunning is returned when starting an already running process.
	ErrRunning = errors.New("process already running")

	// ErrStopped is returned when stopping an already stopped process.
	ErrStopped = errors.New("process already stopped")

	ErrNoReplicationStatus = errors.New("no replication status")
)

// Process represents a MongoDB process.
type Process struct {
	mtx sync.Mutex

	events chan state.DatabaseEvent

	// Replication configuration
	configValue        atomic.Value // *Config
	configAppliedValue atomic.Value // bool

	securityEnabledValue  atomic.Value // bool
	runningValue          atomic.Value // bool
	syncedDownstreamValue atomic.Value // *discoverd.Instance

	ID          string
	Singleton   bool
	Host        string
	Port        string
	BinDir      string
	DataDir     string
	Password    string
	ServerID    uint32
	OpTimeout   time.Duration
	ReplTimeout time.Duration

	Logger log15.Logger

	// cmd is the running system command.
	cmd *Cmd

	// cancelSyncWait cancels the goroutine that is waiting for
	// the downstream to catch up, if running.
	cancelSyncWait func()
}

// NewProcess returns a new instance of Process.
func NewProcess() *Process {
	p := &Process{
		Host:        DefaultHost,
		Port:        DefaultPort,
		BinDir:      DefaultBinDir,
		DataDir:     DefaultDataDir,
		Password:    DefaultPassword,
		OpTimeout:   DefaultOpTimeout,
		ReplTimeout: DefaultReplTimeout,
		Logger:      log15.New("app", "mongodb"),

		events:         make(chan state.DatabaseEvent, 1),
		cancelSyncWait: func() {},
	}
	p.runningValue.Store(false)
	p.configValue.Store((*state.Config)(nil))
	p.events <- state.DatabaseEvent{}
	return p
}

func (p *Process) running() bool         { return p.runningValue.Load().(bool) }
func (p *Process) securityEnabled() bool { return p.securityEnabledValue.Load().(bool) }
func (p *Process) configApplied() bool   { return p.configAppliedValue.Load().(bool) }
func (p *Process) config() *state.Config { return p.configValue.Load().(*state.Config) }

func (p *Process) syncedDownstream() *discoverd.Instance {
	if downstream, ok := p.syncedDownstreamValue.Load().(*discoverd.Instance); ok {
		return downstream
	}
	return nil
}

func (p *Process) ConfigPath() string { return filepath.Join(p.DataDir, "mongod.conf") }

func (p *Process) Reconfigure(config *state.Config) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	switch config.Role {
	case state.RolePrimary:
		if !p.Singleton && config.Downstream == nil {
			return errors.New("missing downstream peer")
		}
	case state.RoleSync, state.RoleAsync:
		if config.Upstream == nil {
			return fmt.Errorf("missing upstream peer")
		}
	case state.RoleNone:
	default:
		return fmt.Errorf("unknown role %v", config.Role)
	}

	if !p.running() {
		p.configValue.Store(config)
		p.configAppliedValue.Store(false)
		return nil
	}

	return p.reconfigure(config)
}

func (p *Process) Start() error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	if p.running() {
		return errors.New("process already running")
	}
	if p.config() == nil {
		return errors.New("unconfigured process")
	}
	if p.config().Role == state.RoleNone {
		return errors.New("start attempted with role 'none'")
	}

	return p.reconfigure(nil)
}

func (p *Process) Stop() error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	if !p.running() {
		return errors.New("process already stopped")
	}
	return p.stop()
}

func (p *Process) Ready() <-chan state.DatabaseEvent {
	return p.events
}

func (p *Process) XLog() xlog.XLog {
	return mongodbxlog.XLog{}
}

func (p *Process) getReplConfig() (*replSetConfig, error) {
	// Connect to local server.
	client, err := p.connectLocal()
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(context.Background())

	// Retrieve replica set configuration.
	var result struct {
		Config replSetConfig `bson:"config"`
	}
	if err := client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "replSetGetConfig", Value: 1}}).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Config, nil
}

func (p *Process) setReplConfig(config replSetConfig) error {
	client, err := p.connectLocal()
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())

	if err := client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "replSetReconfig", Value: config}, {Key: "force", Value: true}}).Err(); err != nil {
		return err
	}
	// XXX(jpg): Prevent mongodb implosion if a reconfigure comes too soon after this one
	time.Sleep(5 * time.Second)
	return nil
}

func clusterSize(clusterState *state.State) int {
	if clusterState.Singleton {
		return 1
	}
	return 2 + len(clusterState.Async)
}

func newMember(addr string, newState *state.State, curIds map[string]int, prio int) replSetMember {
	maxId := clusterSize(newState)
	var id int
	// Keep previous ID if assigned, required for replSetReconfig
	if i, ok := curIds[addr]; ok {
		id = i
	} else {
		// Otherwise assign IDs starting from 0, skipping those in use.
		for i := 0; i < maxId; i++ {
			found := false
			for _, id := range curIds {
				if i == id {
					found = true
				}
			}
			if !found {
				id = i
				break
			}
		}
	}
	curIds[addr] = id // Reserve our newly allocated ID
	return replSetMember{ID: id, Host: addr, Priority: prio}
}

func clusterAddrs(clusterState *state.State) []string {
	addrs := []string{clusterState.Primary.Addr}
	if clusterState.Singleton {
		return addrs
	}
	addrs = append(addrs, clusterState.Sync.Addr)
	for _, n := range clusterState.Async {
		addrs = append(addrs, n.Addr)
	}
	return addrs
}

func (p *Process) replSetConfigFromState(current *replSetConfig, s *state.State) replSetConfig {
	curIds := make(map[string]int, len(current.Members))
	newAddrs := clusterAddrs(s)
	// If any of the current peers are in the new config then preserve their IDs
	for _, m := range current.Members {
		for _, a := range newAddrs {
			if m.Host == a {
				curIds[m.Host] = m.ID
				break
			}
		}
	}
	members := make([]replSetMember, 0, clusterSize(s))
	members = append(members, newMember(s.Primary.Addr, s, curIds, 1))
	// If we aren't running in singleton mode add the other members.
	if !s.Singleton {
		members = append(members, newMember(s.Sync.Addr, s, curIds, 0))
	}
	for _, peer := range s.Async {
		members = append(members, newMember(peer.Addr, s, curIds, 0))
	}
	return replSetConfig{
		ID:      "rs0",
		Members: members,
		Version: current.Version + 1,
	}
}

func (p *Process) reconfigure(config *state.Config) error {
	logger := p.Logger.New("fn", "reconfigure")

	if err := func() error {
		if config != nil && config.Role == state.RoleNone {
			logger.Info("nothing to do", "reason", "null role")
			return nil
		}

		// If we've already applied the same config, we don't need to do anything
		if p.configApplied() && config != nil && p.config() != nil && config.Equal(p.config()) && config.State.Equal(p.config().State) {
			logger.Info("nothing to do", "reason", "config already applied")
			return nil
		}

		// If we're already running and it's just a change from async to sync with the same node, we don't need to restart
		if p.configApplied() && p.running() && p.config() != nil && config != nil &&
			p.config().Role == state.RoleAsync && config.Role == state.RoleSync && config.Upstream.Meta["MONGODB_ID"] == p.config().Upstream.Meta["MONGODB_ID"] {
			logger.Info("nothing to do", "reason", "becoming sync with same upstream")
			return nil
		}
		// Make sure that we don't keep waiting for replication sync while reconfiguring
		p.cancelSyncWait()
		p.syncedDownstreamValue.Store((*discoverd.Instance)(nil))

		if config == nil {
			config = p.config()
		}

		if config.Role == state.RolePrimary {
			return p.assumePrimary(config.Downstream, config.State)
		}

		return p.assumeStandby(config.Upstream, config.Downstream)
	}(); err != nil {
		return err
	}

	// Apply configuration.
	p.configValue.Store(config)
	p.configAppliedValue.Store(true)

	return nil
}

func (p *Process) assumePrimary(downstream *discoverd.Instance, clusterState *state.State) (err error) {
	logger := p.Logger.New("fn", "assumePrimary")
	if downstream != nil {
		logger = logger.New("downstream", downstream.Addr)
	}

	if p.running() {
		if p.config().Role == state.RoleSync {
			logger.Info("promoting to primary")
		}
		logger.Info("updating replica set configuration")
		replSetCurrent, err := p.getReplConfig()
		if err != nil {
			return err
		}
		replSetNew := p.replSetConfigFromState(replSetCurrent, clusterState)
		if err := p.setReplConfig(replSetNew); err != nil {
			return err
		}
		p.waitForSync(downstream)
		return nil
	}

	logger.Info("starting as primary")

	// Assert that the process is not running. This should not occur.
	if p.running() {
		panic(fmt.Sprintf("unexpected state running role=%s", p.config().Role))
	}

	// Begin with both replication and security disabled
	// We will enable both once we either initialise the database or confirm
	// that it has already been initialized.
	p.securityEnabledValue.Store(false)
	if err := p.writeConfig(configData{}); err != nil {
		logger.Error("error writing config", "path", p.ConfigPath(), "err", err)
		return err
	}

	if err := p.start(); err != nil {
		return err
	}

	if err := p.initPrimaryDB(clusterState); err != nil {
		logger.Error("error initialising primary, attempting stop")
		if e := p.stop(); err != nil {
			logger.Debug("ignoring error stopping process", "err", e)
		}
		return err
	}

	if downstream != nil {
		p.waitForSync(downstream)
	}

	return nil
}

func (p *Process) assumeStandby(upstream, downstream *discoverd.Instance) error {
	logger := p.Logger.New("fn", "assumeStandby", "upstream", upstream.Addr)

	if p.running() && !p.securityEnabled() {
		logger.Info("stopping database")
		if err := p.stop(); err != nil {
			return err
		}

	}
	p.securityEnabledValue.Store(true)
	if err := p.writeConfig(configData{ReplicationEnabled: true}); err != nil {
		logger.Error("error writing config", "path", p.ConfigPath(), "err", err)
		return err
	}
	logger.Info("starting up as standby")

	if !p.running() {
		logger.Info("starting database")
		if err := p.start(); err != nil {
			return err
		}
	}

	if downstream != nil {
		p.waitForSync(downstream)
	}

	return nil
}

func (p *Process) replSetGetStatus() (*replSetStatus, error) {
	client, err := p.connectLocal()
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(context.Background())

	return replSetGetStatusQuery(client)
}

func replSetGetStatusQuery(c *mongo.Client) (*replSetStatus, error) {
	var status replSetStatus
	err := c.Database("admin").RunCommand(context.Background(), bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status)
	return &status, err
}

func (p *Process) isReplInitialised() (bool, error) {
	_, err := p.replSetGetStatus()
	if err != nil {
		if cerr, ok := err.(mongo.CommandError); ok {
			switch cerr.Code {
			case 93: // replica set exists but is invalid/we aren't a member
				return true, nil
			case 94: // replica set not yet configured
				return false, nil
			}
			p.Logger.Error("failed to check if replset initialized", "err", err, "code", cerr.Code)
			return false, err
		}
		return false, err
	}
	return true, nil
}

func (p *Process) isUserCreated() (bool, error) {
	ctx := context.Background()
	client, err := mongo.Connect(ctx, p.ClientOptions())
	if err != nil {
		return false, err
	}
	defer client.Disconnect(ctx)

	var result struct {
		Users []struct {
			User string `bson:"user"`
		} `bson:"users"`
	}
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: bson.D{{Key: "user", Value: "flynn"}, {Key: "db", Value: "admin"}}}}).Decode(&result)
	if err != nil {
		if cerr, ok := err.(mongo.CommandError); ok && cerr.Code == 13 {
			return false, nil
		}
		return false, err
	}
	return len(result.Users) > 0, nil
}

func (p *Process) createUser() error {
	ctx := context.Background()
	client, err := mongo.Connect(ctx, p.ClientOptions())
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	if err := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "createUser", Value: "flynn"},
		{Key: "pwd", Value: p.Password},
		{Key: "roles", Value: []bson.M{{"role": "root", "db": "admin"}, {"role": "dbOwner", "db": "admin"}}},
	}).Err(); err != nil {
		return err
	}

	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "fsync", Value: 1}}).Err(); err != nil {
		return err
	}

	return nil
}

// initPrimaryDB initializes the local database with the correct users and plugins.
func (p *Process) initPrimaryDB(clusterState *state.State) error {
	logger := p.Logger.New("fn", "initPrimaryDB")
	logger.Info("initializing primary database")

	// check if admin user has been created
	logger.Info("checking if user has been created")
	created, err := p.isUserCreated()
	if err != nil {
		logger.Error("error checking if user created")
		return err
	}

	// user doesn't exist yet
	if !created {
		logger.Info("stopping database to disable security")
		if err := p.stop(); err != nil {
			return err
		}
		// we need to start the database with both replication and security disabled
		p.securityEnabledValue.Store(false)
		if err := p.writeConfig(configData{}); err != nil {
			logger.Error("error writing config", "path", p.ConfigPath(), "err", err)
			return err
		}
		logger.Info("starting database to create user")
		if err := p.start(); err != nil {
			return err
		}
		logger.Info("creating user")
		if err := p.createUser(); err != nil {
			return err
		}
	}
	logger.Info("stopping database to enable security/replication")
	if err := p.stop(); err != nil {
		return err
	}
	p.securityEnabledValue.Store(true)
	if err := p.writeConfig(configData{ReplicationEnabled: true}); err != nil {
		logger.Error("error writing config", "path", p.ConfigPath(), "err", err)
		return err
	}
	logger.Info("starting database with security enabled")
	if err := p.start(); err != nil {
		return err
	}

	// check if replica set has been initialised
	logger.Info("checking if replica set has been initialised")
	initialized, err := p.isReplInitialised()
	if err != nil {
		logger.Error("error checking replset initialised", "err", err)
		return err
	}
	logger.Info("not initialized, initialising now")
	if !initialized && clusterState != nil {
		if err := p.replSetInitiate(); err != nil {
			logger.Error("error initialising replset", "err", err)
			return err
		}
		// Give MongoDB time to process the initiation before proceeding
		time.Sleep(2 * time.Second)
	}
	logger.Info("getting current replset config")
	replSetCurrent, err := p.getReplConfig()
	if err != nil {
		logger.Error("error getting replset config", "err", err)
		return err
	}

	logger.Info("reconfiguring replset")
	replSetNew := p.replSetConfigFromState(replSetCurrent, clusterState)
	err = p.setReplConfig(replSetNew)
	if err != nil {
		logger.Error("failed to reconfigure replia set", "err", err)
		return err
	}
	return nil
}

func (p *Process) replSetInitiate() error {
	logger := p.Logger.New("fn", "replSetInitiate")
	logger.Info("initialising replica set")
	client, err := p.connectLocal()
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())

	logger.Info("initialising replica set")
	err = client.Database("admin").RunCommand(context.Background(), bson.M{
		"replSetInitiate": replSetConfig{
			ID:      "rs0",
			Members: []replSetMember{{ID: 0, Host: p.addr(), Priority: 1}},
			Version: 1,
		},
	}).Err()
	if err != nil {
		// Code 23 = AlreadyInitialized: the replica set was previously initialized
		// (possibly with a stale member IP). This is not fatal; the subsequent
		// replSetReconfig in initPrimaryDB will fix the member list.
		if cerr, ok := err.(mongo.CommandError); ok && cerr.Code == 23 {
			logger.Info("replica set already initialized, will reconfig", "err", err)
			return nil
		}
		// EOF/io errors can occur when replSetInitiate succeeds and MongoDB
		// closes the connection during the election transition.
		if err.Error() == "EOF" || err.Error() == "end of file" {
			logger.Info("connection closed during replSetInitiate (likely successful)")
			return nil
		}
		logger.Error("failed to initialise replica set", "err", err)
		return err
	}
	return nil
}

func (p *Process) addr() string {
	return net.JoinHostPort(p.Host, p.Port)
}

func (p *Process) connectLocal() (*mongo.Client, error) {
	ctx := context.Background()
	client, err := mongo.Connect(ctx, p.ClientOptions())
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (p *Process) start() error {
	logger := p.Logger.New("fn", "start", "id", p.ID, "port", p.Port)
	logger.Info("starting process")

	cmd := NewCmd(exec.Command(filepath.Join(p.BinDir, "mongod"), "--config", p.ConfigPath()))
	if err := cmd.Start(); err != nil {
		logger.Error("failed to start process", "err", err)
		return err
	}
	p.cmd = cmd
	p.runningValue.Store(true)

	go func() {
		if <-cmd.Stopped(); cmd.Err() != nil {
			logger.Error("process unexpectedly exit", "err", cmd.Err())
			shutdown.ExitWithCode(1)
		}
	}()

	logger.Debug("waiting for process to start")

	timer := time.NewTimer(p.OpTimeout)
	defer timer.Stop()

	for {
		// Connect to server.
		// Retry after sleep if an error occurs.
		if err := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := mongo.Connect(ctx, p.ClientOptions())
			if err != nil {
				return err
			}
			defer client.Disconnect(ctx)

			return client.Ping(ctx, readpref.Primary())
		}(); err != nil {
			select {
			case <-timer.C:
				logger.Error("timed out waiting for process to start", "err", err)
				if err := p.stop(); err != nil {
					logger.Error("error stopping process", "err", err)
				}
				return err
			default:
				logger.Debug("ignoring error connecting to mongodb", "err", err)
				time.Sleep(checkInterval)
				continue
			}
		}

		logger.Debug("process started")
		return nil
	}
}

func (p *Process) stop() error {
	logger := p.Logger.New("fn", "stop")
	logger.Info("stopping mongodb")

	p.cancelSyncWait()

	logger.Info("attempting graceful shutdown")
	client, err := p.connectLocal()
	if err == nil {
		defer client.Disconnect(context.Background())
		err := client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "shutdown", Value: 1}, {Key: "force", Value: true}}).Err()
		if err == nil || err == io.EOF {
			select {
			case <-time.After(p.OpTimeout):
				logger.Error("timed out waiting for graceful shutdown, proceeding to kill")
			case <-p.cmd.Stopped():
				logger.Info("database gracefully shutdown")
				p.runningValue.Store(false)
				return nil
			}
		} else {
			logger.Error("error running shutdown command", "err", err)
		}
	} else {
		logger.Error("error connecting to mongodb", "err", err)
	}

	// Attempt to kill.
	logger.Debug("stopping daemon forcefully")
	if err := p.cmd.Stop(); err != nil {
		logger.Error("error stopping command", "err", err)
	}

	// Wait for cmd to stop or timeout.
	select {
	case <-time.After(p.OpTimeout):
		return errors.New("unable to kill process")
	case <-p.cmd.Stopped():
		p.runningValue.Store(false)
		return nil
	}
}

func (p *Process) Info() (*client.DatabaseInfo, error) {
	logger := p.Logger.New("fn", "Info")
	info := &client.DatabaseInfo{
		Config:           p.config(),
		Running:          p.running(),
		SyncedDownstream: p.syncedDownstream(),
	}
	xlog, err := p.XLogPosition()
	info.XLog = string(xlog)
	if err != nil {
		logger.Error("error getting xlog")
		return info, err
	}
	info.UserExists, err = p.userExists()
	if err != nil {
		logger.Error("error checking userExists")
		return info, err
	}
	info.ReadWrite, err = p.isReadWrite()
	if err != nil {
		logger.Error("error checking isReadWrite")
		return info, err
	}
	return info, err
}

func (p *Process) isReadWrite() (bool, error) {
	if !p.running() {
		return false, nil
	}
	status, err := p.replSetGetStatus()
	return status.MyState == Primary, err
}

func (p *Process) userExists() (bool, error) {
	if !p.running() {
		return false, errors.New("mongod is not running")
	}

	client, err := p.connectLocal()
	if err != nil {
		return false, err
	}
	defer client.Disconnect(context.Background())

	type user struct {
		ID       string `bson:"_id"`
		User     string `bson:"user"`
		Database string `bson:"db"`
	}

	var userInfo struct {
		Users []user `bson:"users"`
		Ok    int    `bson:"ok"`
	}

	if err := client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "usersInfo", Value: bson.M{"user": "flynn", "db": "admin"}}}).Decode(&userInfo); err != nil {
		return false, err
	}

	for _, u := range userInfo.Users {
		if u.User == "flynn" && u.Database == "admin" {
			return true, nil
		}
	}

	return false, nil
}

func (p *Process) waitForSyncInner(downstream *discoverd.Instance, stopCh, doneCh chan struct{}) {
	defer close(doneCh)

	startTime := time.Now().UTC()
	logger := p.Logger.New(
		"fn", "waitForSync",
		"sync_name", downstream.Meta["MONGODB_ID"],
		"start_time", log15.Lazy{Fn: func() time.Time { return startTime }},
	)

	logger.Info("waiting for downstream replication to catch up")
	defer logger.Info("finished waiting for downstream replication")

	for {
		logger.Debug("checking downstream sync")

		// Check if "wait sync" has been canceled.
		select {
		case <-stopCh:
			logger.Debug("canceled, stopping")
			return
		default:
		}

		// get repl status
		status, err := p.replSetGetStatus()
		if err != nil {
			logger.Error("error getting replSetStatus")
			startTime = time.Now().UTC()
			select {
			case <-stopCh:
				logger.Debug("canceled, stopping")
				return
			case <-time.After(checkInterval):
			}
			continue
		}

		var synced bool
		for _, m := range status.Members {
			if m.Name == downstream.Addr && m.State == Secondary {
				synced = true
			}
		}

		if synced {
			p.syncedDownstreamValue.Store(downstream)
			break
		}
		elapsedTime := time.Since(startTime)

		if elapsedTime > p.ReplTimeout {
			logger.Error("error checking replication status", "err", "downstream unable to make forward progress")
			return
		}

		logger.Debug("continuing replication check")
		select {
		case <-stopCh:
			logger.Debug("canceled, stopping")
			return
		case <-time.After(checkInterval):
		}
	}

}

// waitForSync waits for downstream sync in goroutine
func (p *Process) waitForSync(downstream *discoverd.Instance) {
	p.Logger.Debug("waiting for downstream sync")

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})

	var once sync.Once
	p.cancelSyncWait = func() {
		once.Do(func() { close(stopCh); <-doneCh })
	}

	go p.waitForSyncInner(downstream, stopCh, doneCh)
}

// ClientOptions returns client options for connecting to the local process as the "flynn" user.
func (p *Process) ClientOptions() *options.ClientOptions {
	localhost := net.JoinHostPort("127.0.0.1", p.Port)
	opts := options.Client().
		SetHosts([]string{localhost}).
		SetConnectTimeout(5 * time.Second).
		SetDirect(true).
		SetReadPreference(readpref.Nearest())

	if p.securityEnabled() {
		opts.SetHosts([]string{p.addr()})
		opts.SetAuth(options.Credential{
			AuthSource: "admin",
			Username:   "flynn",
			Password:   p.Password,
		})
	}
	return opts
}

func (p *Process) XLogPosition() (xlog.Position, error) {
	status, err := p.replSetGetStatus()
	if err != nil {
		return p.XLog().Zero(), nil
	}
	return p.xlogPosFromStatus(p.addr(), status)
}

func (p *Process) xlogPosFromStatus(member string, status *replSetStatus) (xlog.Position, error) {
	for _, m := range status.Members {
		if m.Name == member {
			return xlog.Position(strconv.FormatUint(uint64(m.Optime.Timestamp.T)<<32|uint64(m.Optime.Timestamp.I), 10)), nil
		}
	}
	return p.XLog().Zero(), fmt.Errorf("error getting xlog, couldn't find member in replSetStatus")
}

func (p *Process) writeConfig(d configData) error {
	d.ID = p.ID
	d.Port = p.Port
	d.DataDir = p.DataDir
	d.SecurityEnabled = p.securityEnabled()

	f, err := os.Create(p.ConfigPath())
	if err != nil {
		return err
	}
	defer f.Close()

	return configTemplate.Execute(f, d)
}

type configData struct {
	ID                 string
	Port               string
	DataDir            string
	SecurityEnabled    bool
	ReplicationEnabled bool
}

var configTemplate = template.Must(template.New("mongod.conf").Parse(`
storage:
  dbPath: {{.DataDir}}
  engine: wiredTiger
  wiredTiger:
    engineConfig:
      cacheSizeGB: 1

# systemLog:
#  destination: file
#  path: {{.DataDir}}/mongod.log
#  logAppend: true

net:
  port: {{.Port}}
  bindIpAll: true

{{if .SecurityEnabled}}
security:
  keyFile: {{.DataDir}}/Keyfile
  authorization: enabled
{{end}}

{{if .ReplicationEnabled}}
replication:
  replSetName: rs0
{{end}}
`[1:]))
