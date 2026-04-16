package vxlan

import (
	"fmt"
	"net"
	"syscall"

	log "github.com/golang/glog"
	"github.com/vishvananda/netlink"

	"github.com/flynn/flynn/flannel/pkg/ip"
)

type vxlanDeviceAttrs struct {
	vni       uint32
	name      string
	vtepIndex int
	vtepAddr  net.IP
	vtepPort  int
}

type vxlanDevice struct {
	link       *netlink.Vxlan
	desiredMAC net.HardwareAddr
}

func newVXLANDevice(devAttrs *vxlanDeviceAttrs) (*vxlanDevice, error) {
	link := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: devAttrs.name,
		},
		VxlanId:      int(devAttrs.vni),
		VtepDevIndex: devAttrs.vtepIndex,
		SrcAddr:      devAttrs.vtepAddr,
		Port:         devAttrs.vtepPort,
		Learning:     false,
	}

	link, err := ensureLink(link)
	if err != nil {
		return nil, err
	}

	// Generate a unique MAC address derived from the VTEP IP to ensure
	// each node's flannel.1 device has a distinct hardware address.
	// Without this, nodes cloned from the same image get identical MACs
	// which breaks VXLAN forwarding.
	//
	// IMPORTANT: Only set the MAC if it differs from the current one.
	// Setting the MAC (even to the same value) causes the kernel to
	// flush all ARP neighbor entries on the interface, which breaks
	// overlay connectivity when flannel instances crash-loop and
	// repeatedly reuse the existing device.
	if ip4 := devAttrs.vtepAddr.To4(); ip4 != nil {
		mac := net.HardwareAddr{0x02, 0x42, ip4[0], ip4[1], ip4[2], ip4[3]}
		currentMAC := link.HardwareAddr
		if !macEqual(currentMAC, mac) {
			if err := netlink.LinkSetHardwareAddr(link, mac); err != nil {
				// If setting MAC while UP fails, try bringing the link down first
				log.Warningf("failed to set MAC on %s while UP: %v; retrying with link down", link.Name, err)
				if err2 := netlink.LinkSetDown(link); err2 != nil {
					log.Warningf("failed to bring %s down: %v", link.Name, err2)
				}
				if err2 := netlink.LinkSetHardwareAddr(link, mac); err2 != nil {
					log.Errorf("failed to set unique MAC on %s even after link down: %v", link.Name, err2)
				} else {
					link.HardwareAddr = mac
					log.Infof("set VXLAN device %s MAC to %s (after link down, derived from VTEP IP %s)", link.Name, mac, devAttrs.vtepAddr)
				}
				if err2 := netlink.LinkSetUp(link); err2 != nil {
					log.Warningf("failed to bring %s back up: %v", link.Name, err2)
				}
			} else {
				link.HardwareAddr = mac
				log.Infof("set VXLAN device %s MAC to %s (derived from VTEP IP %s)", link.Name, mac, devAttrs.vtepAddr)
			}
		} else {
			log.Infof("VXLAN device %s already has correct MAC %s, skipping set", link.Name, mac)
		}
	}

	return &vxlanDevice{
		link:       link,
		desiredMAC: link.HardwareAddr,
	}, nil
}

func ensureLink(vxlan *netlink.Vxlan) (*netlink.Vxlan, error) {
	err := netlink.LinkAdd(vxlan)
	if err == syscall.EEXIST {
		// it's ok if the device already exists as long as config is similar
		existing, err := netlink.LinkByName(vxlan.Name)
		if err != nil {
			return nil, err
		}

		incompat := vxlanLinksIncompat(vxlan, existing)
		if incompat == "" {
			log.Infof("reusing existing %q device", vxlan.Name)
			return existing.(*netlink.Vxlan), nil
		}

		// delete existing
		log.Warningf("%q already exists with incompatible configuration: %v; recreating device", vxlan.Name, incompat)
		if err = netlink.LinkDel(existing); err != nil {
			return nil, fmt.Errorf("failed to delete interface: %v", err)
		}

		// create new
		if err = netlink.LinkAdd(vxlan); err != nil {
			return nil, fmt.Errorf("failed to create vxlan interface: %v", err)
		}
	} else if err != nil {
		return nil, err
	}

	ifindex := vxlan.Index
	link, err := netlink.LinkByIndex(vxlan.Index)
	if err != nil {
		return nil, fmt.Errorf("can't locate created vxlan device with index %v", ifindex)
	}
	var ok bool
	if vxlan, ok = link.(*netlink.Vxlan); !ok {
		return nil, fmt.Errorf("created vxlan device with index %v is not vxlan", ifindex)
	}

	return vxlan, nil
}

func (dev *vxlanDevice) Configure(ipn ip.IP4Net) error {
	if err := setAddr4(dev.link, ipn.ToIPNet()); err != nil {
		return err
	}

	if err := netlink.LinkSetUp(dev.link); err != nil {
		return fmt.Errorf("failed to set interface %s to UP state: %s", dev.link.Attrs().Name, err)
	}

	// Re-apply the desired MAC address after LinkSetUp, because some kernels
	// regenerate the VXLAN MAC when the interface transitions to UP state.
	if dev.desiredMAC != nil {
		current, _ := netlink.LinkByIndex(dev.link.Index)
		if current != nil && !macEqual(current.Attrs().HardwareAddr, dev.desiredMAC) {
			log.Infof("MAC changed after LinkSetUp (got %s, want %s), re-applying", current.Attrs().HardwareAddr, dev.desiredMAC)
			if err := netlink.LinkSetHardwareAddr(dev.link, dev.desiredMAC); err != nil {
				log.Warningf("failed to re-apply MAC on %s: %v", dev.link.Name, err)
			} else {
				dev.link.HardwareAddr = dev.desiredMAC
			}
		}
	}

	// explicitly add a route since there might be a route for a subnet already
	// installed by Docker and then it won't get auto added
	route := netlink.Route{
		LinkIndex: dev.link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       ipn.Network().ToIPNet(),
	}
	if err := netlink.RouteAdd(&route); err != nil && err != syscall.EEXIST {
		return fmt.Errorf("failed to add route (%s -> %s): %v", ipn.Network().String(), dev.link.Attrs().Name, err)
	}

	return nil
}

func (dev *vxlanDevice) Destroy() {
	netlink.LinkDel(dev.link)
}

// macEqual compares two hardware addresses for equality.
func macEqual(a, b net.HardwareAddr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (dev *vxlanDevice) MACAddr() net.HardwareAddr {
	return dev.link.HardwareAddr
}

func (dev *vxlanDevice) MTU() int {
	return dev.link.MTU
}

type neigh struct {
	MAC net.HardwareAddr
	IP  ip.IP4
}

func (dev *vxlanDevice) AddL2(n neigh) error {
	log.Infof("calling NeighSet (L2/FDB): %v, %v", n.IP, n.MAC)
	return netlink.NeighSet(&netlink.Neigh{
		LinkIndex:    dev.link.Index,
		State:        netlink.NUD_PERMANENT,
		Family:       syscall.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		IP:           n.IP.ToIP(),
		HardwareAddr: n.MAC,
	})
}

func (dev *vxlanDevice) DelL2(n neigh) error {
	log.Infof("calling NeighDel: %v, %v", n.IP, n.MAC)
	return netlink.NeighDel(&netlink.Neigh{
		LinkIndex:    dev.link.Index,
		Family:       syscall.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		IP:           n.IP.ToIP(),
		HardwareAddr: n.MAC,
	})
}

func (dev *vxlanDevice) AddL3(n neigh) error {
	log.Infof("calling NeighSet: %v, %v", n.IP, n.MAC)
	return netlink.NeighSet(&netlink.Neigh{
		LinkIndex:    dev.link.Index,
		State:        netlink.NUD_PERMANENT,
		Type:         syscall.RTN_UNICAST,
		IP:           n.IP.ToIP(),
		HardwareAddr: n.MAC,
	})
}

func (dev *vxlanDevice) DelL3(n neigh) error {
	log.Infof("calling NeighDel: %v, %v", n.IP, n.MAC)
	return netlink.NeighDel(&netlink.Neigh{
		LinkIndex:    dev.link.Index,
		State:        netlink.NUD_PERMANENT,
		Type:         syscall.RTN_UNICAST,
		IP:           n.IP.ToIP(),
		HardwareAddr: n.MAC,
	})
}

func (dev *vxlanDevice) AddRoute(subnet ip.IP4Net) error {
	route := &netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Dst:   subnet.ToIPNet(),
		Gw:    subnet.IP.ToIP(),
	}

	log.Infof("calling RouteAdd: %s", subnet)
	return netlink.RouteAdd(route)
}

func (dev *vxlanDevice) DelRoute(subnet ip.IP4Net) error {
	route := &netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Dst:   subnet.ToIPNet(),
		Gw:    subnet.IP.ToIP(),
	}
	log.Infof("calling RouteDel: %s", subnet)
	return netlink.RouteDel(route)
}

func vxlanLinksIncompat(l1, l2 netlink.Link) string {
	if l1.Type() != l2.Type() {
		return fmt.Sprintf("link type: %v vs %v", l1.Type(), l2.Type())
	}

	v1 := l1.(*netlink.Vxlan)
	v2 := l2.(*netlink.Vxlan)

	if v1.VxlanId != v2.VxlanId {
		return fmt.Sprintf("vni: %v vs %v", v1.VxlanId, v2.VxlanId)
	}

	if v1.VtepDevIndex > 0 && v2.VtepDevIndex > 0 && v1.VtepDevIndex != v2.VtepDevIndex {
		return fmt.Sprintf("vtep (external) interface: %v vs %v", v1.VtepDevIndex, v2.VtepDevIndex)
	}

	if len(v1.SrcAddr) > 0 && len(v2.SrcAddr) > 0 && !v1.SrcAddr.Equal(v2.SrcAddr) {
		return fmt.Sprintf("vtep (external) IP: %v vs %v", v1.SrcAddr, v2.SrcAddr)
	}

	if len(v1.Group) > 0 && len(v2.Group) > 0 && !v1.Group.Equal(v2.Group) {
		return fmt.Sprintf("group address: %v vs %v", v1.Group, v2.Group)
	}

	if v1.L2miss != v2.L2miss {
		return fmt.Sprintf("l2miss: %v vs %v", v1.L2miss, v2.L2miss)
	}

	if v1.Port > 0 && v2.Port > 0 && v1.Port != v2.Port {
		return fmt.Sprintf("port: %v vs %v", v1.Port, v2.Port)
	}

	return ""
}

// sets IP4 addr on link removing any existing ones first
func setAddr4(link *netlink.Vxlan, ipn *net.IPNet) error {
	addrs, err := netlink.AddrList(link, syscall.AF_INET)
	if err != nil {
		return err
	}

	addr := netlink.Addr{IPNet: ipn}
	existing := false
	for _, old := range addrs {
		if old.IPNet.String() == addr.IPNet.String() {
			existing = true
			continue
		}
		if err = netlink.AddrDel(link, &old); err != nil {
			return fmt.Errorf("failed to delete IPv4 addr %s from %s", old.String(), link.Attrs().Name)
		}
	}

	if !existing {
		if err = netlink.AddrAdd(link, &addr); err != nil {
			return fmt.Errorf("failed to add IP address %s to %s: %s", ipn.String(), link.Attrs().Name, err)
		}
	}

	return nil
}
