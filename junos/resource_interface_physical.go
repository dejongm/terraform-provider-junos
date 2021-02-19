package junos

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

type interfacePhysicalOptions struct {
	trunk       bool
	vlanTagging bool
	aeMinLink   int
	vlanNative  int
	aeLacp      string
	aeLinkSpeed string
	description string
	v8023ad     string
	vlanMembers []string
	esi         []map[string]interface{}
}

func resourceInterfacePhysical() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceInterfacePhysicalCreate,
		ReadContext:   resourceInterfacePhysicalRead,
		UpdateContext: resourceInterfacePhysicalUpdate,
		DeleteContext: resourceInterfacePhysicalDelete,
		Importer: &schema.ResourceImporter{
			State: resourceInterfacePhysicalImport,
		},
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				ForceNew: true,
				Required: true,
				ValidateFunc: func(v interface{}, k string) (ws []string, errors []error) {
					value := v.(string)
					if strings.Count(value, ".") > 0 {
						errors = append(errors, fmt.Errorf(
							"%q in %q cannot have a dot", value, k))
					}

					return
				},
			},
			"no_disable_on_destroy": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"ae_lacp": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "",
				ValidateFunc: validation.StringInSlice([]string{"active", "passive"}, false),
			},
			"ae_link_speed": {
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.StringInSlice([]string{"100m", "1g", "8g", "10g", "40g", "50g", "80g", "100g"}, false),
			},
			"ae_minimum_links": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"esi": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"mode": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"all-active", "single-active"}, false),
						},
						"auto_derive_lacp": {
							Type:          schema.TypeBool,
							Optional:      true,
							ConflictsWith: []string{"esi.0.identifier"},
						},
						"df_election_type": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: validation.StringInSlice([]string{"mod", "preference"}, false),
						},
						"identifier": {
							Type:          schema.TypeString,
							Optional:      true,
							ConflictsWith: []string{"esi.0.auto_derive_lacp"},
							ValidateFunc: validation.StringMatch(regexp.MustCompile(
								`^([\\d\\w]{2}:){10}[\\d\\w]{2}$`), "bad format or length"),
						},
						"source_bmac": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: validation.IsMACAddress,
						},
					},
				},
			},
			"ether802_3ad": {
				Type:     schema.TypeString,
				Optional: true,
				ValidateFunc: func(v interface{}, k string) (ws []string, errors []error) {
					value := v.(string)
					if !strings.HasPrefix(value, "ae") {
						errors = append(errors, fmt.Errorf(
							"%q in %q isn't an ae interface", value, k))
					}

					return
				},
			},
			"trunk": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"vlan_members": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"vlan_native": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntBetween(1, 4094),
			},
			"vlan_tagging": {
				Type:     schema.TypeBool,
				Optional: true,
			},
		},
	}
}

func resourceInterfacePhysicalCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	sess := m.(*Session)
	jnprSess, err := sess.startNewSession()
	if err != nil {
		return diag.FromErr(err)
	}
	defer sess.closeSession(jnprSess)
	sess.configLock(jnprSess)
	ncInt, emptyInt, err := checkInterfacePhysicalNCEmpty(d.Get("name").(string), m, jnprSess)
	if err != nil {
		sess.configClear(jnprSess)

		return diag.FromErr(err)
	}
	if !ncInt && !emptyInt {
		sess.configClear(jnprSess)

		return diag.FromErr(fmt.Errorf("interface %s already configured", d.Get("name").(string)))
	}
	if ncInt {
		if err := delInterfaceNC(d, m, jnprSess); err != nil {
			sess.configClear(jnprSess)

			return diag.FromErr(err)
		}
	}
	if err := setInterfacePhysical(d, m, jnprSess); err != nil {
		sess.configClear(jnprSess)

		return diag.FromErr(err)
	}
	var diagWarns diag.Diagnostics
	warns, err := sess.commitConf("create resource junos_interface_physical", jnprSess)
	appendDiagWarns(&diagWarns, warns)
	if err != nil {
		sess.configClear(jnprSess)

		return append(diagWarns, diag.FromErr(err)...)
	}
	ncInt, emptyInt, err = checkInterfacePhysicalNCEmpty(d.Get("name").(string), m, jnprSess)
	if err != nil {
		return append(diagWarns, diag.FromErr(err)...)
	}
	if ncInt {
		return append(diagWarns, diag.FromErr(fmt.Errorf("interface %v always disable after commit "+
			"=> check your config", d.Get("name").(string)))...)
	}
	if emptyInt {
		intExists, err := checkInterfaceExists(d.Get("name").(string), m, jnprSess)
		if err != nil {
			return append(diagWarns, diag.FromErr(err)...)
		}
		if !intExists {
			return append(diagWarns, diag.FromErr(fmt.Errorf("interface %v not exists and "+
				"config can't found after commit => check your config", d.Get("name").(string)))...)
		}
	}
	d.SetId(d.Get("name").(string))

	return append(diagWarns, resourceInterfacePhysicalReadWJnprSess(d, m, jnprSess)...)
}
func resourceInterfacePhysicalRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	sess := m.(*Session)
	jnprSess, err := sess.startNewSession()
	if err != nil {
		return diag.FromErr(err)
	}
	defer sess.closeSession(jnprSess)

	return resourceInterfacePhysicalReadWJnprSess(d, m, jnprSess)
}
func resourceInterfacePhysicalReadWJnprSess(
	d *schema.ResourceData, m interface{}, jnprSess *NetconfObject) diag.Diagnostics {
	mutex.Lock()
	ncInt, emptyInt, err := checkInterfacePhysicalNCEmpty(d.Get("name").(string), m, jnprSess)
	if err != nil {
		mutex.Unlock()

		return diag.FromErr(err)
	}
	if ncInt {
		d.SetId("")
		mutex.Unlock()

		return nil
	}
	if emptyInt {
		intExists, err := checkInterfaceExists(d.Get("name").(string), m, jnprSess)
		if err != nil {
			mutex.Unlock()

			return diag.FromErr(err)
		}
		if !intExists {
			d.SetId("")
			mutex.Unlock()

			return nil
		}
	}
	interfaceOpt, err := readInterfacePhysical(d.Get("name").(string), m, jnprSess)
	mutex.Unlock()
	if err != nil {
		return diag.FromErr(err)
	}
	fillInterfacePhysicalData(d, interfaceOpt)

	return nil
}
func resourceInterfacePhysicalUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	d.Partial(true)
	sess := m.(*Session)
	jnprSess, err := sess.startNewSession()
	if err != nil {
		return diag.FromErr(err)
	}
	defer sess.closeSession(jnprSess)
	sess.configLock(jnprSess)
	if err := delInterfacePhysicalOpts(d, m, jnprSess); err != nil {
		sess.configClear(jnprSess)

		return diag.FromErr(err)
	}
	if err := setInterfacePhysical(d, m, jnprSess); err != nil {
		sess.configClear(jnprSess)

		return diag.FromErr(err)
	}
	var diagWarns diag.Diagnostics
	warns, err := sess.commitConf("update resource junos_interface_physical", jnprSess)
	appendDiagWarns(&diagWarns, warns)
	if err != nil {
		sess.configClear(jnprSess)

		return append(diagWarns, diag.FromErr(err)...)
	}
	d.Partial(false)

	return append(diagWarns, resourceInterfacePhysicalReadWJnprSess(d, m, jnprSess)...)
}
func resourceInterfacePhysicalDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	sess := m.(*Session)
	jnprSess, err := sess.startNewSession()
	if err != nil {
		return diag.FromErr(err)
	}
	defer sess.closeSession(jnprSess)
	sess.configLock(jnprSess)
	if err := delInterfacePhysical(d, m, jnprSess); err != nil {
		sess.configClear(jnprSess)

		return diag.FromErr(err)
	}
	var diagWarns diag.Diagnostics
	warns, err := sess.commitConf("delete resource junos_interface_physical", jnprSess)
	appendDiagWarns(&diagWarns, warns)
	if err != nil {
		sess.configClear(jnprSess)

		return append(diagWarns, diag.FromErr(err)...)
	}
	if !d.Get("no_disable_on_destroy").(bool) {
		intExists, err := checkInterfaceExists(d.Get("name").(string), m, jnprSess)
		if err != nil {
			appendDiagWarns(&diagWarns, []error{err})
		} else if intExists {
			err = addInterfacePhysicalNC(d.Get("name").(string), m, jnprSess)
			if err != nil {
				sess.configClear(jnprSess)

				return append(diagWarns, diag.FromErr(err)...)
			}
			_, err = sess.commitConf("disable(NC) resource junos_interface_physical", jnprSess)
			if err != nil {
				sess.configClear(jnprSess)

				return append(diagWarns, diag.FromErr(err)...)
			}
		}
	}

	return diagWarns
}
func resourceInterfacePhysicalImport(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	if strings.Count(d.Id(), ".") != 0 {
		return nil, fmt.Errorf("name of interface %s need to doesn't have a dot", d.Id())
	}
	sess := m.(*Session)
	jnprSess, err := sess.startNewSession()
	if err != nil {
		return nil, err
	}
	defer sess.closeSession(jnprSess)
	result := make([]*schema.ResourceData, 1)
	ncInt, emptyInt, err := checkInterfacePhysicalNCEmpty(d.Id(), m, jnprSess)
	if err != nil {
		return nil, err
	}
	if ncInt {
		return nil, fmt.Errorf("interface '%v' is disabled, import is not possible", d.Id())
	}
	if emptyInt {
		intExists, err := checkInterfaceExists(d.Id(), m, jnprSess)
		if err != nil {
			return nil, err
		}
		if !intExists {
			return nil, fmt.Errorf("don't find interface with id '%v' (id must be <name>)", d.Id())
		}
	}
	interfaceOpt, err := readInterfacePhysical(d.Id(), m, jnprSess)
	if err != nil {
		return nil, err
	}
	if tfErr := d.Set("name", d.Id()); tfErr != nil {
		panic(tfErr)
	}
	fillInterfacePhysicalData(d, interfaceOpt)

	result[0] = d

	return result, nil
}

func checkInterfacePhysicalNCEmpty(interFace string, m interface{}, jnprSess *NetconfObject) (
	ncInt bool, emtyInt bool, errFunc error) {
	sess := m.(*Session)
	intConfig, err := sess.command("show configuration interfaces "+interFace+" | display set relative", jnprSess)
	if err != nil {
		return false, false, err
	}
	intConfigLines := make([]string, 0)
	// remove unused lines
	for _, item := range strings.Split(intConfig, "\n") {
		// show parameters root on interface exclude unit parameters (except ethernet-switching)
		if strings.HasPrefix(item, "set unit") && !strings.Contains(item, "ethernet-switching") {
			continue
		}
		if strings.Contains(item, "<configuration-output>") {
			continue
		}
		if strings.Contains(item, "</configuration-output>") {
			break
		}
		if item == "" {
			continue
		}
		intConfigLines = append(intConfigLines, item)
	}
	if len(intConfigLines) == 0 {
		return false, true, nil
	}
	intConfig = strings.Join(intConfigLines, "\n")
	if sess.junosGroupIntDel != "" {
		if intConfig == "set apply-groups "+sess.junosGroupIntDel {
			return true, false, nil
		}
	}
	if intConfig == "set description NC\nset disable" ||
		intConfig == "set disable\nset description NC" {
		return true, false, nil
	}
	if intConfig == emptyWord {
		return false, true, nil
	}

	return false, false, nil
}

func addInterfacePhysicalNC(interFace string, m interface{}, jnprSess *NetconfObject) error {
	var err error
	if sess := m.(*Session); sess.junosGroupIntDel == "" {
		err = sess.configSet([]string{"set interfaces " + interFace + " disable description NC"}, jnprSess)
	} else {
		err = sess.configSet([]string{"set interfaces " + interFace +
			" apply-groups " + sess.junosGroupIntDel}, jnprSess)
	}
	if err != nil {
		return err
	}

	return nil
}

func checkInterfaceExists(interFace string, m interface{}, jnprSess *NetconfObject) (bool, error) {
	sess := m.(*Session)
	rpcIntName := "<get-interface-information><interface-name>" + interFace +
		"</interface-name></get-interface-information>"
	reply, err := sess.commandXML(rpcIntName, jnprSess)
	if err != nil {
		return false, err
	}
	if strings.Contains(reply, " not found\n") {
		return false, nil
	}

	return true, nil
}

func setInterfacePhysical(d *schema.ResourceData, m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	configSet := make([]string, 0)
	setPrefix := "set interfaces " + d.Get("name").(string) + " "
	configSet = append(configSet, setPrefix)
	if d.Get("ae_lacp").(string) != "" {
		if !strings.HasPrefix(d.Get("name").(string), "ae") {
			return fmt.Errorf("ae_lacp invalid for this interface")
		}
		configSet = append(configSet, setPrefix+
			"aggregated-ether-options lacp "+d.Get("ae_lacp").(string))
	}
	if d.Get("ae_link_speed").(string) != "" {
		if !strings.HasPrefix(d.Get("name").(string), "ae") {
			return fmt.Errorf("ae_link_speed invalid for this interface")
		}
		configSet = append(configSet, setPrefix+
			"aggregated-ether-options link-speed "+d.Get("ae_link_speed").(string))
	}
	if d.Get("ae_minimum_links").(int) > 0 {
		if !strings.HasPrefix(d.Get("name").(string), "ae") {
			return fmt.Errorf("ae_minimum_links invalid for this interface")
		}
		configSet = append(configSet, setPrefix+
			"aggregated-ether-options minimum-links "+strconv.Itoa(d.Get("ae_minimum_links").(int)))
	}
	if d.Get("description").(string) != "" {
		configSet = append(configSet, setPrefix+"description \""+d.Get("description").(string)+"\"")
	}
	if err := setIntEsi(setPrefix, d.Get("esi").([]interface{}), m, jnprSess); err != nil {
		return err
	}
	if v := d.Get("name").(string); strings.HasPrefix(v, "ae") {
		aggregatedCount, err := interfaceAggregatedCountSearchMax(v, "ae-1", v, m, jnprSess)
		if err != nil {
			return err
		}
		configSet = append(configSet, "set chassis aggregated-devices ethernet device-count "+aggregatedCount)
	} else if d.Get("ether802_3ad").(string) != "" {
		configSet = append(configSet, setPrefix+"ether-options 802.3ad "+
			d.Get("ether802_3ad").(string))
		configSet = append(configSet, setPrefix+"gigether-options 802.3ad "+
			d.Get("ether802_3ad").(string))
		oldAE := "ae-1"
		if d.HasChange("ether802_3ad") {
			oldAEtf, _ := d.GetChange("ether802_3ad")
			if oldAEtf.(string) != "" {
				oldAE = oldAEtf.(string)
			}
		}
		aggregatedCount, err := interfaceAggregatedCountSearchMax(d.Get("ether802_3ad").(string), oldAE,
			d.Get("name").(string), m, jnprSess)
		if err != nil {
			return err
		}
		configSet = append(configSet, "set chassis aggregated-devices ethernet device-count "+aggregatedCount)
	}
	if d.Get("trunk").(bool) {
		configSet = append(configSet, setPrefix+"unit 0 family ethernet-switching interface-mode trunk")
	}
	for _, v := range d.Get("vlan_members").([]interface{}) {
		configSet = append(configSet, setPrefix+
			"unit 0 family ethernet-switching vlan members "+v.(string))
	}
	if d.Get("vlan_native").(int) != 0 {
		configSet = append(configSet, setPrefix+"native-vlan-id "+strconv.Itoa(d.Get("vlan_native").(int)))
	}
	if d.Get("vlan_tagging").(bool) {
		configSet = append(configSet, setPrefix+"vlan-tagging")
	}
	if err := sess.configSet(configSet, jnprSess); err != nil {
		return err
	}

	return nil
}
func setIntEsi(setPrefix string, esiParams []interface{},
	m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	configSet := make([]string, 0)

	for _, v := range esiParams {
		if v != nil {
			m := v.(map[string]interface{})
			switch {
			case m["mode"].(string) != "":
				configSet = append(configSet, setPrefix+"esi "+m["mode"].(string))
			case m["auto_derive_lacp"].(bool):
				configSet = append(configSet, setPrefix+"esi auto-derive lacp")
			case m["df_election_type"].(string) != "":
				configSet = append(configSet, setPrefix+"esi df-election-type "+m["df_election_type"].(string))
			case m["identifier"].(string) != "":
				configSet = append(configSet, setPrefix+"esi "+m["identifier"].(string))
			case m["source_bmac"].(string) != "":
				configSet = append(configSet, setPrefix+"esi source-bmac "+m["source_bmac"].(string))
			}
		}
	}

	return sess.configSet(configSet, jnprSess)
}
func readInterfacePhysical(interFace string, m interface{}, jnprSess *NetconfObject) (interfacePhysicalOptions, error) {
	sess := m.(*Session)
	var confRead interfacePhysicalOptions

	intConfig, err := sess.command("show configuration interfaces "+interFace+" | display set relative", jnprSess)
	if err != nil {
		return confRead, err
	}
	if intConfig != emptyWord {
		for _, item := range strings.Split(intConfig, "\n") {
			if strings.Contains(item, " unit ") && !strings.Contains(item, "ethernet-switching") {
				continue
			}
			if strings.Contains(item, "<configuration-output>") {
				continue
			}
			if strings.Contains(item, "</configuration-output>") {
				break
			}
			itemTrim := strings.TrimPrefix(item, setLineStart)
			switch {
			case strings.HasPrefix(itemTrim, "aggregated-ether-options lacp "):
				confRead.aeLacp = strings.TrimPrefix(itemTrim, "aggregated-ether-options lacp ")
			case strings.HasPrefix(itemTrim, "aggregated-ether-options link-speed "):
				confRead.aeLinkSpeed = strings.TrimPrefix(itemTrim, "aggregated-ether-options link-speed ")
			case strings.HasPrefix(itemTrim, "aggregated-ether-options minimum-links "):
				confRead.aeMinLink, err = strconv.Atoi(strings.TrimPrefix(itemTrim,
					"aggregated-ether-options minimum-links "))
				if err != nil {
					return confRead, fmt.Errorf("failed to convert value from '%s' to integer : %w", itemTrim, err)
				}
			case strings.HasPrefix(itemTrim, "description "):
				confRead.description = strings.Trim(strings.TrimPrefix(itemTrim, "description "), "\"")
			case strings.HasPrefix(itemTrim, "esi "):
				if err := readIntEsi(&confRead, itemTrim); err != nil {
					return confRead, err
				}
			case strings.HasPrefix(itemTrim, "ether-options 802.3ad "):
				confRead.v8023ad = strings.TrimPrefix(itemTrim, "ether-options 802.3ad ")
			case strings.HasPrefix(itemTrim, "gigether-options 802.3ad "):
				confRead.v8023ad = strings.TrimPrefix(itemTrim, "gigether-options 802.3ad ")
			case strings.HasPrefix(itemTrim, "native-vlan-id"):
				confRead.vlanNative, err = strconv.Atoi(strings.TrimPrefix(itemTrim, "native-vlan-id "))
				if err != nil {
					return confRead, fmt.Errorf("failed to convert value from '%s' to integer : %w", itemTrim, err)
				}
			case itemTrim == "unit 0 family ethernet-switching interface-mode trunk":
				confRead.trunk = true
			case strings.HasPrefix(itemTrim, "unit 0 family ethernet-switching vlan members"):
				confRead.vlanMembers = append(confRead.vlanMembers, strings.TrimPrefix(itemTrim,
					"unit 0 family ethernet-switching vlan members "))
			case itemTrim == "vlan-tagging":
				confRead.vlanTagging = true
			default:
				continue
			}
		}
	}

	return confRead, nil
}
func readIntEsi(confRead *interfacePhysicalOptions, item string) error {
	itemTrim := strings.TrimPrefix(item, "esi ")
	if len(confRead.esi) == 0 {
		confRead.esi = append(confRead.esi, map[string]interface{}{
			"mode":             "",
			"auto_derive_lacp": false,
			"df_election_type": "",
			"identifier":       "",
			"source_bmac":      "",
		})
	}
	var err error
	identifier, err := regexp.MatchString(`^([\d\w]{2}:){9}[\d\w]{2}`, itemTrim)
	if err != nil {
		return fmt.Errorf("esi_identifier regexp error: %w", err)
	}
	switch {
	case identifier:
		confRead.esi[0]["identifier"] = itemTrim
	case itemTrim == "all-active" || itemTrim == "single-active":
		confRead.esi[0]["mode"] = itemTrim
	case strings.HasPrefix(itemTrim, "df-election-type "):
		confRead.esi[0]["df_election_type"] = strings.TrimPrefix(itemTrim, "df-election-type ")
	case strings.HasPrefix(itemTrim, "source-bmac "):
		confRead.esi[0]["source_bmac"] = strings.TrimPrefix(itemTrim, "source-bmac ")
	case itemTrim == "auto-derive lacp":
		confRead.esi[0]["auto_derive_lacp"] = true
	}

	return nil
}
func delInterfacePhysical(d *schema.ResourceData, m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	if err := checkInterfacePhysicalContainsUnit(d.Get("name").(string), m, jnprSess); err != nil {
		return err
	}
	if err := sess.configSet([]string{"delete interfaces " + d.Get("name").(string)}, jnprSess); err != nil {
		return err
	}
	if v := d.Get("name").(string); strings.HasPrefix(v, "ae") {
		aggregatedCount, err := interfaceAggregatedCountSearchMax("ae-1", v, v, m, jnprSess)
		if err != nil {
			return err
		}
		if aggregatedCount == "0" {
			err = sess.configSet([]string{"delete chassis aggregated-devices ethernet device-count"}, jnprSess)
			if err != nil {
				return err
			}
		} else {
			err = sess.configSet([]string{"set chassis aggregated-devices ethernet device-count " + aggregatedCount}, jnprSess)
			if err != nil {
				return err
			}
		}
	} else if d.Get("ether802_3ad").(string) != "" {
		lastAEchild, err := interfaceAggregatedLastChild(d.Get("ether802_3ad").(string), d.Get("name").(string), m, jnprSess)
		if err != nil {
			return err
		}
		if lastAEchild {
			aggregatedCount, err := interfaceAggregatedCountSearchMax("ae-1", d.Get("ether802_3ad").(string),
				d.Get("name").(string), m, jnprSess)
			if err != nil {
				return err
			}
			if aggregatedCount == "0" {
				err = sess.configSet([]string{"delete chassis aggregated-devices ethernet device-count"}, jnprSess)
				if err != nil {
					return err
				}
			} else {
				err = sess.configSet([]string{"set chassis aggregated-devices ethernet device-count " + aggregatedCount}, jnprSess)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func checkInterfacePhysicalContainsUnit(interFace string, m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	intConfig, err := sess.command("show configuration interfaces "+interFace+" | display set relative", jnprSess)
	if err != nil {
		return err
	}
	for _, item := range strings.Split(intConfig, "\n") {
		if strings.Contains(item, "<configuration-output>") {
			continue
		}
		if strings.Contains(item, "</configuration-output>") {
			break
		}
		if strings.HasPrefix(item, "set unit") {
			if strings.Contains(item, "ethernet-switching") {
				continue
			}

			return fmt.Errorf("interface %s is used for other son unit interface", interFace)
		}
	}

	return nil
}
func delInterfaceNC(d *schema.ResourceData, m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	configSet := make([]string, 0, 1)
	delPrefix := "delete interfaces " + d.Get("name").(string) + " "
	if sess.junosGroupIntDel != "" {
		configSet = append(configSet, delPrefix+"apply-groups "+sess.junosGroupIntDel)
	}
	configSet = append(configSet, delPrefix+"description")
	configSet = append(configSet, delPrefix+"disable")
	if err := sess.configSet(configSet, jnprSess); err != nil {
		return err
	}

	return nil
}
func delInterfacePhysicalOpts(d *schema.ResourceData, m interface{}, jnprSess *NetconfObject) error {
	sess := m.(*Session)
	configSet := make([]string, 0, 1)
	delPrefix := "delete interfaces " + d.Get("name").(string) + " "
	configSet = append(configSet,
		delPrefix+"aggregated-ether-options",
		delPrefix+"esi",
		delPrefix+"ether-options 802.3ad",
		delPrefix+"gigether-options 802.3ad",
		delPrefix+"native-vlan-id",
		delPrefix+"unit 0 family ethernet-switching interface-mode",
		delPrefix+"unit 0 family ethernet-switching vlan members",
		delPrefix+"vlan-tagging",
	)
	if err := sess.configSet(configSet, jnprSess); err != nil {
		return err
	}

	return nil
}

func fillInterfacePhysicalData(d *schema.ResourceData, interfaceOpt interfacePhysicalOptions) {
	if tfErr := d.Set("ae_lacp", interfaceOpt.aeLacp); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("ae_link_speed", interfaceOpt.aeLinkSpeed); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("ae_minimum_links", interfaceOpt.aeMinLink); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("esi", interfaceOpt.esi); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("description", interfaceOpt.description); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("ether802_3ad", interfaceOpt.v8023ad); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("trunk", interfaceOpt.trunk); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("vlan_members", interfaceOpt.vlanMembers); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("vlan_native", interfaceOpt.vlanNative); tfErr != nil {
		panic(tfErr)
	}
	if tfErr := d.Set("vlan_tagging", interfaceOpt.vlanTagging); tfErr != nil {
		panic(tfErr)
	}
}

func interfaceAggregatedLastChild(ae, interFace string, m interface{}, jnprSess *NetconfObject) (bool, error) {
	sess := m.(*Session)
	showConf, err := sess.command("show configuration interfaces | display set relative", jnprSess)
	if err != nil {
		return false, err
	}
	lastAE := true
	for _, item := range strings.Split(showConf, "\n") {
		if strings.HasSuffix(item, "ether-options 802.3ad "+ae) &&
			!strings.HasPrefix(item, "set "+interFace+" ") {
			lastAE = false
		}
	}

	return lastAE, nil
}
func interfaceAggregatedCountSearchMax(
	newAE, oldAE, interFace string, m interface{}, jnprSess *NetconfObject) (string, error) {
	sess := m.(*Session)
	newAENum := strings.TrimPrefix(newAE, "ae")
	newAENumInt, err := strconv.Atoi(newAENum)
	if err != nil {
		return "", fmt.Errorf("failed to convert ae interaface '%v' to integer : %w", newAE, err)
	}
	showConf, err := sess.command("show configuration interfaces | display set relative", jnprSess)
	if err != nil {
		return "", err
	}
	listAEFound := make([]string, 0)
	regexpAEchild := regexp.MustCompile(`ether-options 802\.3ad ae\d+$`)
	regexpAEparent := regexp.MustCompile(`^set ae\d+ `)
	for _, line := range strings.Split(showConf, "\n") {
		aeMatchChild := regexpAEchild.MatchString(line)
		aeMatchParent := regexpAEparent.MatchString(line)
		switch {
		case aeMatchChild:
			wordsLine := strings.Fields(line)
			if interFace == oldAE {
				// interfaceAggregatedCountSearchMax called for delete parent interface
				listAEFound = append(listAEFound, wordsLine[len(wordsLine)-1])
			} else if wordsLine[len(wordsLine)-1] != oldAE {
				listAEFound = append(listAEFound, wordsLine[len(wordsLine)-1])
			}
		case aeMatchParent:
			wordsLine := strings.Fields(line)
			if interFace != oldAE {
				// interfaceAggregatedCountSearchMax called for child interface or new parent
				listAEFound = append(listAEFound, wordsLine[1])
			} else if wordsLine[1] != oldAE {
				listAEFound = append(listAEFound, wordsLine[1])
			}
		}
	}
	lastOldAE, err := interfaceAggregatedLastChild(oldAE, interFace, m, jnprSess)
	if err != nil {
		return "", err
	}
	if !lastOldAE {
		listAEFound = append(listAEFound, oldAE)
	}
	if len(listAEFound) > 0 {
		sort.Sort(sortStringsLength(listAEFound))
		lastAeInt, err := strconv.Atoi(strings.TrimPrefix(listAEFound[len(listAEFound)-1], "ae"))
		if err != nil {
			return "", fmt.Errorf("failed to convert internal variable lastAeInt to integer : %w", err)
		}
		if lastAeInt > newAENumInt {
			return strconv.Itoa(lastAeInt + 1), nil
		}
	}

	return strconv.Itoa(newAENumInt + 1), nil
}
