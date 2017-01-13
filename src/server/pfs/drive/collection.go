package drive

import (
	"context"
	"fmt"
	"path"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/gogo/protobuf/proto"
)

const (
	locksPrefix   = "/locks"
	reposPrefix   = "/repos"
	commitsPrefix = "/commits"
	refsPrefix    = "/refs"
)

type ErrNotFound struct {
	Type string
	Name string
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("%s %s not found", e.Type, e.Name)
}

type ErrExists struct {
	Type string
	Name string
}

func (e ErrExists) Error() string {
	return fmt.Sprintf("%s %s already exists", e.Type, e.Name)
}

// repos returns a collection of repos
// Example etcd structure, assuming we have two repos "foo" and "bar":
//   /repos
//     /foo
//     /bar
func (d *driver) repos(ctx context.Context) *collection {
	return &collection{
		ctx:         ctx,
		prefix:      path.Join(d.prefix, reposPrefix),
		locksPrefix: path.Join(d.prefix, locksPrefix, reposPrefix),
		etcdClient:  d.etcdClient,
	}
}

// commits returns a collection of commits
// Example etcd structure, assuming we have two repos "foo" and "bar":
//   /commits
//     /foo
//       /UUID1
//       /UUID2
//     /bar
//       /UUID3
//       /UUID4
func (d *driver) commits(ctx context.Context) collectionFactory {
	return func(repo string) *collection {
		return &collection{
			ctx:         ctx,
			prefix:      path.Join(d.prefix, commitsPrefix, repo),
			locksPrefix: path.Join(d.prefix, locksPrefix, commitsPrefix, repo),
			etcdClient:  d.etcdClient,
		}
	}
}

// commits returns a collection of commits
// Example etcd structure, assuming we have two repos "foo" and "bar",
// each of which has two refs:
//   /refs
//     /foo
//       /master
//       /test
//     /bar
//       /master
//       /test
func (d *driver) refs(ctx context.Context) collectionFactory {
	return func(repo string) *collection {
		return &collection{
			ctx:         ctx,
			prefix:      path.Join(d.prefix, refsPrefix, repo),
			locksPrefix: path.Join(d.prefix, locksPrefix, refsPrefix, repo),
			etcdClient:  d.etcdClient,
		}
	}
}

// collection implements helper functions that makes common operations
// on top of etcd more pleasant to work with.  It's called collection
// because most of our data is modelled as collections, such as repos,
// commits, refs, etc.
type collection struct {
	ctx        context.Context
	etcdClient *etcd.Client
	prefix     string
	// a prefix for locks
	locksPrefix string
	session     *concurrency.Session
	mutex       *concurrency.Mutex
}

// stm converts the collection into a STM collection instead
func (c *collection) stm(_stm concurrency.STM) *stmCollection {
	return &stmCollection{
		collection: c,
		stm:        _stm,
	}
}

// collectionFactory generates collections.  It's mainly used for
// namespaced collections, such as /commits/foo, i.e. commits in
// repo foo.
type collectionFactory func(string) *collection

func (c *collection) path(key string) string {
	return path.Join(c.prefix, key)
}

func (c *collection) Get(key string, val proto.Message) error {
	resp, err := c.etcdClient.Get(c.ctx, c.path(key))
	if err != nil {
		return err
	}
	if resp.Count == 0 {
		return ErrNotFound{c.prefix, key}
	}
	return proto.UnmarshalText(string(resp.Kvs[0].Value), val)
}

func (c *collection) Put(key string, val proto.Message) error {
	valBytes, err := proto.Marshal(val)
	if err != nil {
		return err
	}
	_, err = c.etcdClient.Put(c.ctx, c.path(key), string(valBytes))
	return err
}

func (c *collection) Create(key string, val proto.Message) error {
	fullKey := c.path(key)
	resp, err := c.etcdClient.Txn(c.ctx).If(absent(fullKey)).Then(etcd.OpPut(fullKey, proto.MarshalTextString(val))).Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return ErrExists{c.prefix, key}
	}
	return nil
}

func (c *collection) List(key *string, val proto.Message, iterate func() error) error {
	resp, err := c.etcdClient.Get(c.ctx, c.path(""), etcd.WithPrefix())
	if err != nil {
		return err
	}

	for _, kv := range resp.Kvs {
		*key = string(kv.Key)
		if err := proto.UnmarshalText(string(kv.Value), val); err != nil {
			return err
		}
		if err := iterate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *collection) Delete(key string) error {
	_, err := c.etcdClient.Delete(c.ctx, key)
	return err
}

func (c *collection) Lock() error {
	var err error
	c.session, err = concurrency.NewSession(c.etcdClient)
	if err != nil {
		return err
	}
	c.mutex = concurrency.NewMutex(c.session, c.locksPrefix)
	return c.mutex.Lock(c.ctx)
}

func (c *collection) Unlock() error {
	defer c.session.Close()
	return c.mutex.Unlock(c.ctx)
}

// stmCollection is similar to collection, except that it's implemented
// with an STM (software transactional memory) abstraction
type stmCollection struct {
	*collection
	stm concurrency.STM
}

func (s *stmCollection) path(key string) string {
	return path.Join(s.prefix, key)
}

func (s *stmCollection) Get(key string, val proto.Message) error {
	valStr := s.stm.Get(s.path(key))
	if valStr == "" {
		return ErrNotFound{s.prefix, key}
	}
	return proto.UnmarshalText(valStr, val)
}

func (s *stmCollection) Put(key string, val proto.Message) error {
	s.stm.Put(s.path(key), val.String())
	return nil
}

func (s *stmCollection) Create(key string, val proto.Message) error {
	fullKey := s.path(key)
	valStr := s.stm.Get(fullKey)
	if valStr != "" {
		return ErrExists{s.prefix, key}
	}
	s.stm.Put(fullKey, val.String())
	return nil
}

func (s *stmCollection) Delete(key string) error {
	s.stm.Del(s.path(key))
	return nil
}
