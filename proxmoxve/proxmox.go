package proxmoxve

// This file is most copy & pasted from my private project to
// auto-generate the API client on basis of the JSON described API
// in https://pve.proxmox.com/pve-docs/api-viewer/apidoc.js

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/docker/machine/libmachine/state"
	"github.com/labstack/gommon/log"
	"gopkg.in/resty.v1"
)

// ProxmoxVE open api connection representation
type ProxmoxVE struct {
	// connection parameters
	Username string // root
	password string // must be given
	Realm    string // pam
	Host     string
	Port     int // default 8006

	// not so imported internal stuff
	Node                string // if not present, use first node present
	Prefix              string // if PVE is proxied, this is the added prefix
	CSRFPreventionToken string // filled by the framework
	Ticket              string // filled by the framework

	Version string // ProxmoxVE version of the connected host

	client *resty.Client // resty client
}

// GetProxmoxVEConnectionByValues is a wrapper for GetProxmoxVEConnection with strings as input
func GetProxmoxVEConnectionByValues(username string, password string, realm string, hostname string) (*ProxmoxVE, error) {
	return GetProxmoxVEConnection(&ProxmoxVE{
		Username: username,
		password: password,
		Realm:    realm,
		Host:     hostname,
	})
}

// GetProxmoxVEConnection retrievs a connection to a Proxmox VE host
func GetProxmoxVEConnection(data *ProxmoxVE) (*ProxmoxVE, error) {
	if data.Port == 0 {
		data.Port = 8006
	}

	if len(data.password) == 0 {
		return data, fmt.Errorf("You have to provide a password")
	}

	if len(data.Username) == 0 {
		data.Username = "root"
	}
	if len(data.Realm) == 0 {
		data.Realm = "pam"
	}

	data.client = resty.New()

	//data.client.SetDebug(true)
	data.client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	//data.client.SetTimeout(time.Duration(3 * time.Second))

	outp, err := data.accessTicketPost(&AccessTicketPostParameter{
		Username: data.Username,
		Realm:    data.Realm,
		Password: data.password,
	})

	if err != nil {
		return data, err
	}

	if outp.Csrfpreventiontoken == "" {
		return nil, fmt.Errorf("Could not extract CSRFPreventionToken")
	}

	data.CSRFPreventionToken = outp.Csrfpreventiontoken
	data.client.SetHeader("CSRFPreventionToken", outp.Csrfpreventiontoken)
	data.client.SetCookie(&http.Cookie{
		Name:  "PVEAuthCookie",
		Value: outp.Ticket,
	})
	data.Ticket = outp.Ticket

	ver, err := data.versionGet()
	if err != nil {
		return data, err
	}

	data.Version = ver.Version

	return data, nil
}

func (p ProxmoxVE) EnableDebugging() {
	p.client.SetDebug(true)
}

func (p ProxmoxVE) getURL(str string) string {
	return fmt.Sprintf("https://%s:%d/%sapi2/json%s", p.Host, p.Port, p.Prefix, str)
}

// idea taken from https://gist.github.com/tonyhb/5819315
func (p ProxmoxVE) structToStringMap(i interface{}) map[string]string {
	retval := make(map[string]string, 0)
	if i == nil {
		return retval
	}
	iVal := reflect.ValueOf(i).Elem()
	typ := iVal.Type()
	for i := 0; i < iVal.NumField(); i++ {
		f := iVal.Field(i)
		// You ca use tags here...
		// tag := typ.Field(i).Tag.Get("tagname")
		// Convert each type into a string for the url.Values string map
		var v string
		switch f.Interface().(type) {
		case int, int8, int16, int32, int64:
			v = strconv.FormatInt(f.Int(), 10)
		case uint, uint8, uint16, uint32, uint64:
			v = strconv.FormatUint(f.Uint(), 10)
		case float32:
			v = strconv.FormatFloat(f.Float(), 'f', 4, 32)
		case float64:
			v = strconv.FormatFloat(f.Float(), 'f', 4, 64)
		case []byte:
			v = string(f.Bytes())
		case string:
			v = f.String()
		case bool:
			// map to Proxmox VE API boolean, which is int with 1 for true and 0 for false
			if f.Bool() {
				v = "1"
			} else {
				v = "0"
			}
		}
		if len(v) > 0 {
			retval[strings.ToLower(typ.Field(i).Name)] = v
		}
	}
	// MARTINH
	fmt.Printf("structToStringMap: %s\n\n", retval)
	fmt.Printf("structToStringMap: %+v\n\n", retval)
	return retval
}

func (p ProxmoxVE) post(input interface{}, output interface{}, path string) error {
	return p.runMethod("post", input, output, path)
}

func (p ProxmoxVE) get(input interface{}, output interface{}, path string) error {
	return p.runMethod("get", input, output, path)
}

func (p ProxmoxVE) put(input interface{}, output interface{}, path string) error {
	return p.runMethod("put", input, output, path)
}

func (p ProxmoxVE) delete(input interface{}, output interface{}, path string) error {
	return p.runMethod("delete", input, output, path)
}

func (p ProxmoxVE) runMethod(method string, input interface{}, output interface{}, path string) error {
	var response *resty.Response
	var err error

	switch method {
	case "get":
		response, err = p.client.R().SetQueryParams(p.structToStringMap(input)).Get(p.getURL(path))
		
	case "post":
		response, err = p.client.R().SetFormData(p.structToStringMap(input)).Post(p.getURL(path))
	case "put":
		response, err = p.client.R().SetQueryParams(p.structToStringMap(input)).Put(p.getURL(path))
	case "delete":
		response, err = p.client.R().SetQueryParams(p.structToStringMap(input)).Delete(p.getURL(path))
	default:
		return fmt.Errorf("method '%s' not known", method)
	}

	if err != nil {
		return err
	}

	code := response.StatusCode()
	if code < 200 || code > 300 {
		fmt.Printf("RESPONSE OBJECT:\n\n%+v\n", response)
		return fmt.Errorf("status code was '%d' and error is\n%s", code, response.Status())
	}

	if output == nil {
		return nil
	}

	var f map[string]interface{}

	err = json.Unmarshal([]byte(response.String()), &f)
	if err != nil {
		return err
	}
	zz, err := json.Marshal(f["data"])
	if err != nil {
		return err
	}

	err = json.Unmarshal(zz, &output)

	return err
}

// AccessTicketPostParameter represents the input data for /access/ticket
// Original Description:
// Create or verify authentication ticket.
type AccessTicketPostParameter struct {
	Privs    string // optional
	Realm    string // optional
	Username string
	OTP      string // optional
	Password string
	Path     string // optional
}

// AccessTicketReturnParameter represents the returned data from /access/ticket
// Original Description:
// Create or verify authentication ticket.
type AccessTicketReturnParameter struct {
	Username            string
	Csrfpreventiontoken string
	Ticket              string
}

// AccessTicketPost access the API
// Create or verify authentication ticket.
func (p ProxmoxVE) accessTicketPost(input *AccessTicketPostParameter) (*AccessTicketReturnParameter, error) {
	path := "/access/ticket"
	outp := AccessTicketReturnParameter{}
	err := p.post(input, &outp, path)
	return &outp, err
}

// VersionReturnParameter represents the returned data from /version
// Original Description:
// API version details. The result also includes the global datacenter confguration.
type VersionReturnParameter struct {
	RepoID  string
	Version string
	Release string
}

// VersionGet access the API
// API version details. The result also includes the global datacenter confguration.
func (p ProxmoxVE) versionGet() (*VersionReturnParameter, error) {
	path := "/version"
	outp := VersionReturnParameter{}
	err := p.get(nil, &outp, path)
	return &outp, err
}

// NodesNodeStorageStorageContentPostParameter represents the input data for /nodes/{node}/storage/{storage}/content
// Original Description:
// Allocate disk images.
type NodesNodeStorageStorageContentPostParameter struct {
	Filename string // The name of the file to create.
	Size     string // Size in kilobyte (1024 bytes). Optional suffixes 'M' (megabyte, 1024K) and 'G' (gigabyte, 1024M)
	VMID     string // Specify owner VM
	Format   string // optional,
}

// NodesNodeStorageStorageContentPost access the API
// Allocate disk images.
func (p ProxmoxVE) NodesNodeStorageStorageContentPost(node string, storage string, input *NodesNodeStorageStorageContentPostParameter) error {
	path := fmt.Sprintf("/nodes/%s/storage/%s/content", node, storage)
	err := p.post(input, nil, path)
	return err
}


// NodesNodeStorageStorageContentDelete access the API
// Remove disk images.
func (p ProxmoxVE) NodesNodeStorageStorageContentDelete(node string, storage string, volume string) error {
path := fmt.Sprintf("/nodes/%s/storage/%s/content/%s", node, storage, volume)
err := p.delete(nil, nil, path)
return err
}

// ClusterNextIDGet Get next free VMID. If you pass an VMID it will raise an error if the ID is already used.
func (p ProxmoxVE) ClusterNextIDGet(id int) (vmid string, err error) {
	path := "/cluster/nextid"

	if id == 0 {
		fmt.Println("aca 0")

		err = p.get(nil, &vmid, path)
	} else {
		fmt.Println("acato con ", id)

		err = p.get(id, &vmid, path)
	}
	return vmid, err
}

// NodesNodeQemuPostParameter represents the input data for /nodes/{node}/qemu
// Original Description:
// Create or restore a virtual machine.
type NodesNodeQemuPostParameter struct {
	VMID      string // The (unique) ID of the VM.
	Memory    int    // optional, Amount of RAM for the VM in MB. This is the maximum available memory when you use the balloon device.
	Autostart string // optional, Automatic restart after crash (currently ignored).
	Agent     string // optional, Enable/disable Qemu GuestAgent.
	Net0      string
	Name      string // optional, Set a name for the VM. Only used on the configuration web interface.
	SCSI0     string // optional, Use volume as VIRTIO hard disk (n is 0 to 15).
	Onboot    string
	Ostype    string // optional, Specify guest operating system.
	KVM       string // optional, Enable/disable KVM hardware virtualization.
	Pool      string // optional, Add the VM to the specified pool.
	Sockets   string // optional, The number of CPU sockets.
	Cores     string // optional, The number of cores per socket.
	Cdrom     string // optional, This is an alias for option -ide2
	SshKeys   string // optional, cloud-init: Setup public SSH keys (one key per l ine, OpenSSH format)
	CPU       string // optional, Emulated CPU type from list with flags if present
	Numa      int    // optional, Enable/disable NUMA.
	Citype    string // optional, Cloud-Init Type nocloud for linux configdrive2 for windows
 	Ciuser    string // optional, username to change ssh keys and pass instead of image's configured default user
	IDE0      string //
}

type nNodesNodeQemuPostParameter struct {
	VMID            string   // The (unique) ID of the VM.
	Node            string   // The cluster node name
	Acpi            bool     // optional, Enable/disable ACPI.
	Agent           string   // optional, Enable/disable Qemu GuestAgent.
	Archive         string   // optional, The backup file.
	Args            string   // optional, Arbitrary arguments passed to kvm.
	Autostart       string   // optional, Automatic restart after crash (currently ignored).
	Balloon         int      // optional, Amount of target RAM for the VM in MB. Using zero disables the ballon driver.
	Bios            string   // optional, Select BIOS implementation.
	Boot            string   // optional, Boot on floppy (a), hard disk (c), CD-ROM (d), or network (n).
	Bootdisk        string   // optional, Enable booting from specified disk.
	Cdrom           string   // optional, This is an alias for option -ide2
	Citype          string // optional, Cloud-Init Type nocloud for linux configdrive2 for windows
	Ciuser          string // optional, username to change ssh keys and pass instead of image's configured default user
	Cores           string   // optional, The number of cores per socket.
	CPU             string   // optional, Emulated CPU type.
	Cpulimit        int      // optional, Limit of CPU usage.
	Cpuunits        int      // optional, CPU weight for a VM.
	Description     string   // optional, Description for the VM. Only used on the configuration web interface. This is saved as comment inside the configuration file.
	Force           bool     // optional, Allow to overwrite existing VM.
	Freeze          bool     // optional, Freeze CPU at startup (use 'c' monitor command to start execution).
	Hostpci         []string // optional, Map host PCI devices into guest.
	Hotplug         string   // optional, Selectively enable hotplug features. This is a comma separated list of hotplug features: 'network', 'disk', 'cpu', 'memory' and 'usb'. Use '0' to disable hotplug completely. Value '1' is an alias for the default 'network,disk,usb'.
	Hugepages       string   // optional, Enable/disable hugepages memory.
	IDE             []string // optional, Use volume as IDE hard disk or CD-ROM (n is 0 to 3).
	Keyboard        string   // optional, Keybord layout for vnc server. Default is read from the '/etc/pve/datacenter.conf' configuration file.
	KVM             bool     // optional, Enable/disable KVM hardware virtualization.
	Localtime       bool     // optional, Set the real time clock to local time. This is enabled by default if ostype indicates a Microsoft OS.
	Lock            string   // optional, Lock/unlock the VM.
	Machine         string   // optional, Specific the Qemu machine type.
	Memory          string   // optional, Amount of RAM for the VM in MB. This is the maximum available memory when you use the balloon device.
	MigrateDowntime int      // optional, Set maximum tolerated downtime (in seconds) for migrations.
	MigrateSpeed    int      // optional, Set maximum speed (in MB/s) for migrations. Value 0 is no limit.
	Name            string   // optional, Set a name for the VM. Only used on the configuration web interface.
	Net0            string
	//NET             []string // optional, Specify network devices.
	// numa is defined more than once, we ignore the []string parameter
	Numa           int      // optional, Enable/disable NUMA.
	//Numa           []string // optional, NUMA topology.
	Onboot         string     // optional, Specifies whether a VM will be started during system bootup.
	Ostype         string   // optional, Specify guest operating system.
	Parallel       []string // optional, Map host parallel devices (n is 0 to 2).
	Pool           string   // optional, Add the VM to the specified pool.
	Protection     bool     // optional, Sets the protection flag of the VM. This will disable the remove VM and remove disk operations.
	Reboot         bool     // optional, Allow reboot. If set to '0' the VM exit on reboot.
	Sata           []string // optional, Use volume as SATA hard disk or CD-ROM (n is 0 to 5).
	Scsi           []string // optional, Use volume as SCSI hard disk or CD-ROM (n is 0 to 13).
	Scsihw         string   // optional, SCSI controller model
	Serial         []string // optional, Create a serial device inside the VM (n is 0 to 3)
	Shares         int      // optional, Amount of memory shares for auto-ballooning. The larger the number is, the more memory this VM gets. Number is relative to weights of all other running VMs. Using zero disables auto-ballooning
	Smbios1        string   // optional, Specify SMBIOS type 1 fields.
	SMP            int      // optional, The number of CPUs. Please use option -sockets instead.
	Sockets        string   // optional, The number of CPU sockets.
	SshKeys        string   // optional, cloud-init: Setup public SSH keys (one key per l ine, OpenSSH format)
	Startdate      string   // optional, Set the initial date of the real time clock. Valid format for date are: 'now' or '2006-06-17T16:01:21' or '2006-06-17'.
	Startup        string   // optional, Startup and shutdown behavior. Order is a non-negative number defining the general startup order. Shutdown in done with reverse ordering. Additionally you can set the 'up' or 'down' delay in seconds, which specifies a delay to wait before the next VM is started or stopped.
	Storage        string   // optional, Default storage.
	Tablet         bool     // optional, Enable/disable the USB tablet device.
	TDF            bool     // optional, Enable/disable time drift fix.
	Template       bool     // optional, Enable/disable Template.
	Unique         bool     // optional, Assign a unique random ethernet address.
	Unused         []string // optional, Reference to unused volumes. This is used internally, and should not be modified manually.
	USB            []string // optional, Configure an USB device (n is 0 to 4).
	Vcpus          int      // optional, Number of hotplugged vcpus.
	VGA            string   // optional, Select the VGA type.
	Virtio         []string // optional, Use volume as VIRTIO hard disk (n is 0 to 15).
	VMstatestorage string   // optional, Default storage for VM state volumes/files.
	Watchdog       string   // optional, Create a virtual hardware watchdog device.
}

// NodesNodeQemuPost access the API
// Create or restore a virtual machine.
func (p ProxmoxVE) NodesNodeQemuPost(node string, input *NodesNodeQemuPostParameter) error {
	path := fmt.Sprintf("/nodes/%s/qemu", node)
	data, err1 := json.Marshal(input)
	if err1 != nil {

	}
	fmt.Printf("%s\n", data)

	err := p.post(input, nil, path)
	return err
}

// NodesNodeQemuVMIDStatusStartPost access the API
// Start virtual machine.
func (p ProxmoxVE) NodesNodeQemuVMIDStatusStartPost(node string, vmid string) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/status/start", node, vmid)
	err := p.post(nil, nil, path)
	return err
}

// NodesNodeQemuVMIDAgentPostParameter represents the input data for /nodes/{node}/qemu/{vmid}/agent
// Original Description:
// Execute Qemu Guest Agent commands.
type NodesNodeQemuVMIDAgentPostParameter struct {
	Command string // The QGA command.
}

// NodesNodeQemuVMIDAgentPost access the API
// Execute Qemu Guest Agent commands.
func (p ProxmoxVE) NodesNodeQemuVMIDAgentPost(node string, vmid string, input *NodesNodeQemuVMIDAgentPostParameter) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/agent", node, vmid)
	err := p.post(input, nil, path)
	return err
}

// NodesNodeQemuVMIDDelete access the API
// Destroy the vm (also delete all used/owned volumes).
func (p ProxmoxVE) NodesNodeQemuVMIDDelete(node string, vmid string) error {
	p.NodesNodeQemuVMIDStatusStopPost(node, vmid)
	time.Sleep(time.Second)

	path := fmt.Sprintf("/nodes/%s/qemu/%s", node, vmid)
	err := p.delete(nil, nil, path)
	return err
}

// NodesNodeQemuVMIDStatusStopPost access the API
// Stop virtual machine. The qemu process will exit immediately. Thisis akin to pulling the power plug of a running computer and may damage the VM data
func (p ProxmoxVE) NodesNodeQemuVMIDStatusStopPost(node string, vmid string) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/status/stop", node, vmid)
	err := p.post(nil, nil, path)
	return err
}

func unmarshallString(data string, value string) (string, error) {
	var f map[string]interface{}
	err := json.Unmarshal([]byte(data), &f)
	if err != nil {
		return "", err
	}
	zz, err := json.Marshal(f["value"])
	if err != nil {
		return "", err
	}
	return string(zz), err
}

type IPReturn struct {
	Data struct {
		Result []struct {
			HardwareAddress string `json:"hardware-address"`
			Name            string `json:"name"`
			IPAdresses      []struct {
				IPAddress     string `json:"ip-address"`
				IPAddressType string `json:"ip-address-type"`
				Prefix        int    `json:"prefix"`
			} `json:"ip-addresses"`
		} `json:"result"`
	} `json:"data"`
}

// GetEth0IPv4 access the API
func (p ProxmoxVE) GetEth0IPv4(node string, vmid string) (string, error) {
	input := NodesNodeQemuVMIDAgentPostParameter{Command: "network-get-interfaces"}
	path := fmt.Sprintf("/nodes/%s/qemu/%s/agent", node, vmid)

	response, err := p.client.R().SetQueryParams(p.structToStringMap(&input)).Post(p.getURL(path))

	var a IPReturn
	resp := response.String()
	err = json.Unmarshal([]byte(resp), &a)
	if err != nil {
		return "", err
	}
	for _, nic := range a.Data.Result {
		if nic.Name == "eth0" {
			for _, ip := range nic.IPAdresses {
				if ip.IPAddressType == "ipv4" {
					return ip.IPAddress, nil
				}
			}
		}
	}

	return "", err
}

// NodesNodeQemuVMIDStatusCurrentGet access the API
// Get virtual machine status.
func (p ProxmoxVE) NodesNodeQemuVMIDStatusCurrentGet(node string, vmid string) (state.State, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/status/current", node, vmid)
	response, err := p.client.R().Get(p.getURL(path))
	var f map[string]interface{}

	err = json.Unmarshal([]byte(response.String()), &f)
	if err != nil {
		return state.Paused, err
	}

	zz, err := json.Marshal(f["data"])
	if err != nil {
		return state.Paused, err
	}

	err = json.Unmarshal(zz, &f)
	if err != nil {
		return state.Paused, err
	}

	log.Warnf("Status is '%s'", f["status"])

	switch f["status"] {
	case "running":
		return state.Running, nil
	case "stopped":
		return state.Stopped, nil
	}

	return state.Error, nil
}

type IntBool bool

func (bit IntBool) UnmarshalJSON(data []byte) error {
	asString := string(data)
	if asString == "1" || asString == "true" {
		bit = true
	} else if asString == "0" || asString == "false" {
		bit = false
	} else {
		return errors.New(fmt.Sprintf("Boolean unmarshal error: invalid input %s", asString))
	}
	return nil
}

type StorageReturn struct {
	Data []struct {
		Active  int     `json:"active"`
		Avail   int     `json:"avail"`
		Content string  `json:"content"`
		Enabled IntBool `json:"enabled"`
		Shared  IntBool `json:"shared"`
		Storage string  `json:"storage"`
		Total   int     `json:"total"`
		Type    string  `json:"type"`
		Used    int     `json:"used"`
	} `json:"data"`
}

// GetEth0IPv4 access the API
func (p ProxmoxVE) GetStorageType(node string, storagename string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/storage", node)

	response, err := p.client.R().Get(p.getURL(path))

	var a StorageReturn
	resp := response.String()
	err = json.Unmarshal([]byte(resp), &a)
	if err != nil {
		return "", err
	}

	for _, storage := range a.Data {
		if storage.Storage == storagename {
			return storage.Type, nil
		}
	}
	return "", errors.New(fmt.Sprintf("storage '%s' not found", storagename))
}
