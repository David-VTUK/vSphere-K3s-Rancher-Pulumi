package main

import (
	"encoding/base64"
	"fmt"
	"github.com/pulumi/pulumi-vsphere/sdk/v3/go/vsphere"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"io/ioutil"
	"strconv"
	"strings"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {

		// Point to stack config file
		conf := config.New(ctx, "")

		// Extract variable values
		portGroupName := conf.Get("portGroupName")
		datacenterName := conf.Get("datacenterName")
		datastoreName := conf.Get("datastoreName")
		resourcepoolName := conf.Get("resourcepoolName")
		templatenameName := conf.Get("templatenameName")
		vmPrefixName := conf.Get("vmPrefixName")
		rancherURLName := conf.Get("rancherURLName")
		metallbRangeStart := conf.Get("metallbRangeStart")
		metallbRangeFinish := conf.Get("metallbRangeFinish")

		// Metadata portion of cloud-init to configure to hostname
		metaData, err := ioutil.ReadFile("./metadata.yaml")

		if err != nil {
			return err
		}

		// Lookup vCenter Datacenter Object
		datacenter, err := vsphere.LookupDatacenter(ctx, &vsphere.LookupDatacenterArgs{
			Name: &datacenterName,
		})

		if err != nil {
			return err
		}

		opt1 := datacenter.Id

		// Lookup vCenter Resourcepool Object
		resourcepool, err := vsphere.LookupResourcePool(ctx, &vsphere.LookupResourcePoolArgs{
			DatacenterId: &opt1,
			Name:         &resourcepoolName,
		})

		if err != nil {
			return err
		}

		// Lookup vCenter Portgroup Object
		network, err := vsphere.GetNetwork(ctx, &vsphere.GetNetworkArgs{
			DatacenterId: &opt1,
			Name:         portGroupName,
		})

		if err != nil {
			return err
		}

		// Lookup vCenter VM Template Object
		template, err := vsphere.LookupVirtualMachine(ctx, &vsphere.LookupVirtualMachineArgs{
			Name:         templatenameName,
			DatacenterId: &opt1,
		})

		if err != nil {
			return err
		}

		// Lookup vCenter Datastore Object
		datastore, err := vsphere.GetDatastore(ctx, &vsphere.GetDatastoreArgs{
			DatacenterId: &opt1,
			Name:         datastoreName,
		})

		if err != nil {
			return err
		}

		// Hold deployed VM's
		var vms []*vsphere.VirtualMachine

		// Create a 3 node k3s cluster with embedded etcd
		for i := 0; i < 3; i++ {

			// Rename VM via cloud-init metadata
			replacedMetaData := strings.Replace(string(metaData), "cloud-vm", vmPrefixName+strconv.Itoa(i+1), -1)
			encodedMetaData := base64.StdEncoding.EncodeToString([]byte(replacedMetaData))

			// Bootstrap k3s install via cloud-init userdata
			var userDataEncoded string

			// If this is the first VM, use it to initialise the cluster and install Rancher, Certmanager, etc
			if i == 0 {
				userData, _ := ioutil.ReadFile("./userdata.yaml")
				//userDataContents := strings.Replace(string(userData),"$RANCHER_URL",rancherURLName, -1)
				userDataReplacer := strings.NewReplacer("$RANCHER_URL", rancherURLName,
					"$METALLB_RANGE_START", metallbRangeStart,
					"$METALLB_RANGE_FINISH", metallbRangeFinish)

				userDataContents := userDataReplacer.Replace(string(userData))

				userDataEncoded = base64.StdEncoding.EncodeToString([]byte(userDataContents))

				vm, err := vsphere.NewVirtualMachine(ctx, vmPrefixName+strconv.Itoa(i), &vsphere.VirtualMachineArgs{
					Memory:         pulumi.Int(4096),
					NumCpus:        pulumi.Int(4),
					DatastoreId:    pulumi.String(datastore.Id),
					Name:           pulumi.String(vmPrefixName + strconv.Itoa(i+1)),
					ResourcePoolId: pulumi.String(resourcepool.Id),
					GuestId:        pulumi.String(template.GuestId),
					ExtraConfig: pulumi.StringMap{"guestinfo.userdata.encoding": pulumi.String("base64"),
						"guestinfo.userdata": pulumi.String(userDataEncoded), "guestinfo.metadata.encoding": pulumi.String("base64"),
						"guestinfo.metadata": pulumi.String(encodedMetaData)},
					Clone: vsphere.VirtualMachineCloneArgs{
						TemplateUuid: pulumi.String(template.Id),
					},
					Disks: vsphere.VirtualMachineDiskArray{vsphere.VirtualMachineDiskArgs{
						Label: pulumi.String("Disk0"),
						Size:  pulumi.Int(50),
					}},
					NetworkInterfaces: vsphere.VirtualMachineNetworkInterfaceArray{vsphere.VirtualMachineNetworkInterfaceArgs{
						NetworkId: pulumi.String(network.Id),
					},
					},
				},
				)

				if err != nil {
					return err
				}

				vms = append(vms, vm)

			} else {

				// Extract the concrete value of the first VM's IP address, required to join subsequent nodes to the cluster
				join := vms[0].DefaultIpAddress.ApplyT(func(ipaddress string) string {
					return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("#cloud-config \nruncmd: \n  - curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC=\"server\" K3S_URL=https://%s:6443 K3S_TOKEN=\"super-secret\" sh -", ipaddress)))
				}).(pulumi.StringOutput)

				vm, err := vsphere.NewVirtualMachine(
					ctx,
					vmPrefixName+strconv.Itoa(i),
					&vsphere.VirtualMachineArgs{
						Memory:         pulumi.Int(4096),
						NumCpus:        pulumi.Int(4),
						DatastoreId:    pulumi.String(datastore.Id),
						Name:           pulumi.String(vmPrefixName + strconv.Itoa(i+1)),
						ResourcePoolId: pulumi.String(resourcepool.Id),
						GuestId:        pulumi.String(template.GuestId),
						ExtraConfig: pulumi.StringMap{"guestinfo.userdata.encoding": pulumi.String("base64"),
							"guestinfo.userdata": join, "guestinfo.metadata.encoding": pulumi.String("base64"),
							"guestinfo.metadata": pulumi.String(encodedMetaData)},
						Clone: vsphere.VirtualMachineCloneArgs{
							TemplateUuid: pulumi.String(template.Id),
						},
						Disks: vsphere.VirtualMachineDiskArray{vsphere.VirtualMachineDiskArgs{
							Label: pulumi.String("Disk0"),
							Size:  pulumi.Int(50),
						}},
						NetworkInterfaces: vsphere.VirtualMachineNetworkInterfaceArray{vsphere.VirtualMachineNetworkInterfaceArgs{
							NetworkId: pulumi.String(network.Id),
						},
						},
					},
					pulumi.DependsOn([]pulumi.Resource{vms[0]}),
				)
				if err != nil {
					return err
				}
				vms = append(vms, vm)
			}
		}

		ctx.Export("Rancher url:", pulumi.String(rancherURLName))
		ctx.Export("Rancher IP (Set DNS)", pulumi.String(metallbRangeStart))
		return nil
	})
}
