//go:build linux

package flows

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	pb "simplemonitor/gen/simplemonitor/v1"
)

// Collector loads the TC program, attaches it to eligible interfaces and turns
// the BPF map into per-interval FlowSamples.
type Collector struct {
	objs   flowsObjects
	differ Differ
	globs  []string
	log    *slog.Logger

	mu       sync.Mutex
	attached map[int]*attachment // by ifindex
}

type attachment struct {
	name   string
	links  []link.Link
	filter []*netlink.BpfFilter // fallback (clsact) filters
}

// New loads the BPF objects. ifaceGlobs restricts the interfaces (empty = every
// up, ethernet-type, non-virtual interface of this network namespace).
func New(ifaceGlobs []string, log *slog.Logger) (*Collector, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w", err)
	}
	c := &Collector{globs: ifaceGlobs, log: log, attached: map[int]*attachment{}}
	if err := loadFlowsObjects(&c.objs, nil); err != nil {
		return nil, fmt.Errorf("load eBPF objects: %w", err)
	}
	if err := c.Refresh(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

var virtualPrefixes = []string{"lo", "veth", "docker", "br-", "cni", "flannel", "cali", "cilium", "lxc", "virbr", "tun", "tap", "wg"}

func (c *Collector) eligible(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || len(i.HardwareAddr) == 0 {
		return false
	}
	if len(c.globs) > 0 {
		for _, g := range c.globs {
			if ok, _ := path.Match(g, i.Name); ok {
				return true
			}
		}
		return false
	}
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(i.Name, p) {
			return false
		}
	}
	return true
}

// Refresh attaches to interfaces that appeared and forgets ones that vanished.
// Safe to call every interval.
func (c *Collector) Refresh() error {
	ifs, err := net.Interfaces()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	present := map[int]bool{}
	for _, i := range ifs {
		present[i.Index] = true
		if _, ok := c.attached[i.Index]; ok || !c.eligible(i) {
			continue
		}
		a, err := c.attach(i)
		if err != nil {
			c.log.Warn("flows: attach failed", "iface", i.Name, "err", err)
			continue
		}
		c.attached[i.Index] = a
		c.log.Info("flows: eBPF attached", "iface", i.Name)
	}
	for idx, a := range c.attached {
		if !present[idx] {
			c.detach(a)
			delete(c.attached, idx)
		}
	}
	if len(c.attached) == 0 {
		return errors.New("no interface eligible for flow capture")
	}
	return nil
}

func (c *Collector) attach(i net.Interface) (*attachment, error) {
	a := &attachment{name: i.Name}
	l1, err1 := link.AttachTCX(link.TCXOptions{Interface: i.Index, Program: c.objs.TcIngress, Attach: ebpf.AttachTCXIngress})
	if err1 == nil {
		l2, err2 := link.AttachTCX(link.TCXOptions{Interface: i.Index, Program: c.objs.TcEgress, Attach: ebpf.AttachTCXEgress})
		if err2 == nil {
			a.links = []link.Link{l1, l2}
			return a, nil
		}
		l1.Close()
		err1 = err2
	}
	// Kernel < 6.6 (or TCX refused): classic clsact + cls_bpf.
	c.log.Debug("flows: TCX unavailable, falling back to clsact", "iface", i.Name, "err", err1)
	if err := c.attachClsact(i.Index, a); err != nil {
		return nil, fmt.Errorf("tcx: %v; clsact: %w", err1, err)
	}
	return a, nil
}

func (c *Collector) attachClsact(idx int, a *attachment) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{LinkIndex: idx, Handle: netlink.MakeHandle(0xffff, 0), Parent: netlink.HANDLE_CLSACT},
		QdiscType:  "clsact",
	}
	if err := netlink.QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("clsact qdisc: %w", err)
	}
	for _, f := range []struct {
		parent uint32
		prog   *ebpf.Program
		name   string
	}{
		{netlink.HANDLE_MIN_INGRESS, c.objs.TcIngress, "sm_ingress"},
		{netlink.HANDLE_MIN_EGRESS, c.objs.TcEgress, "sm_egress"},
	} {
		flt := &netlink.BpfFilter{
			FilterAttrs:  netlink.FilterAttrs{LinkIndex: idx, Parent: f.parent, Handle: 1, Protocol: unix.ETH_P_ALL, Priority: 1},
			Fd:           f.prog.FD(),
			Name:         f.name,
			DirectAction: true,
		}
		if err := netlink.FilterReplace(flt); err != nil {
			c.detach(a)
			return fmt.Errorf("cls_bpf %s: %w", f.name, err)
		}
		a.filter = append(a.filter, flt)
	}
	return nil
}

func (c *Collector) detach(a *attachment) {
	for _, l := range a.links {
		l.Close()
	}
	for _, f := range a.filter {
		_ = netlink.FilterDel(f)
	}
	a.links, a.filter = nil, nil
}

// Collect returns the flow deltas since the previous call.
func (c *Collector) Collect() ([]*pb.FlowSample, error) {
	if err := c.Refresh(); err != nil {
		c.log.Warn("flows: refresh", "err", err)
	}
	cur := map[Key]Counts{}
	var (
		k flowsFlowKey
		v flowsFlowVal
	)
	it := c.objs.Flows.Iterate()
	for it.Next(&k, &v) {
		cur[Key{Src: k.Src, Dst: k.Dst, Proto: k.Proto, Dir: k.Dir}] = Counts{v.Bytes, v.Packets}
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("iterate flows map: %w", err)
	}
	return Samples(c.differ.Diff(cur)), nil
}

// Interfaces lists the interface names currently instrumented.
func (c *Collector) Interfaces() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, a := range c.attached {
		out = append(out, a.name)
	}
	return out
}

// Close detaches everything and frees the BPF objects.
func (c *Collector) Close() {
	c.mu.Lock()
	for _, a := range c.attached {
		c.detach(a)
	}
	c.attached = map[int]*attachment{}
	c.mu.Unlock()
	c.objs.Close()
}
