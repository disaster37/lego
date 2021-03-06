package main

import (
	"bufio"
	"encoding/json"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/codegangsta/cli"
	"github.com/xenolf/lego/acme"
	"github.com/xenolf/lego/providers/dns/cloudflare"
	"github.com/xenolf/lego/providers/dns/digitalocean"
	"github.com/xenolf/lego/providers/dns/dnsimple"
	"github.com/xenolf/lego/providers/dns/dyn"
	"github.com/xenolf/lego/providers/dns/gandi"
	"github.com/xenolf/lego/providers/dns/googlecloud"
	"github.com/xenolf/lego/providers/dns/namecheap"
	"github.com/xenolf/lego/providers/dns/ovh"
	"github.com/xenolf/lego/providers/dns/rfc2136"
	"github.com/xenolf/lego/providers/dns/route53"
	"github.com/xenolf/lego/providers/dns/vultr"
	"github.com/xenolf/lego/providers/http/webroot"
)

func checkFolder(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0700)
	}
	return nil
}

func setup(c *cli.Context) (*Configuration, *Account, *acme.Client) {

	if c.GlobalIsSet("http-timeout") {
		acme.HTTPTimeout = time.Duration(c.GlobalInt("http-timeout")) * time.Second
	}

	if c.GlobalIsSet("dns-timeout") {
		acme.DNSTimeout = time.Duration(c.GlobalInt("dns-timeout")) * time.Second
	}

	err := checkFolder(c.GlobalString("path"))
	if err != nil {
		logger().Fatalf("Could not check/create path: %s", err.Error())
	}

	conf := NewConfiguration(c)
	if len(c.GlobalString("email")) == 0 {
		logger().Fatal("You have to pass an account (email address) to the program using --email or -m")
	}

	//TODO: move to account struct? Currently MUST pass email.
	acc := NewAccount(c.GlobalString("email"), conf)

	keyType, err := conf.KeyType()
	if err != nil {
		logger().Fatal(err.Error())
	}

	client, err := acme.NewClient(c.GlobalString("server"), acc, keyType)
	if err != nil {
		logger().Fatalf("Could not create client: %s", err.Error())
	}

	if len(c.GlobalStringSlice("exclude")) > 0 {
		client.ExcludeChallenges(conf.ExcludedSolvers())
	}

	if c.GlobalIsSet("webroot") {
		provider, err := webroot.NewHTTPProvider(c.GlobalString("webroot"))
		if err != nil {
			logger().Fatal(err)
		}

		client.SetChallengeProvider(acme.HTTP01, provider)

		// --webroot=foo indicates that the user specifically want to do a HTTP challenge
		// infer that the user also wants to exclude all other challenges
		client.ExcludeChallenges([]acme.Challenge{acme.DNS01, acme.TLSSNI01})
	}
	if c.GlobalIsSet("http") {
		if strings.Index(c.GlobalString("http"), ":") == -1 {
			logger().Fatalf("The --http switch only accepts interface:port or :port for its argument.")
		}
		client.SetHTTPAddress(c.GlobalString("http"))
	}

	if c.GlobalIsSet("tls") {
		if strings.Index(c.GlobalString("tls"), ":") == -1 {
			logger().Fatalf("The --tls switch only accepts interface:port or :port for its argument.")
		}
		client.SetTLSAddress(c.GlobalString("tls"))
	}

	if c.GlobalIsSet("dns") {
		var err error
		var provider acme.ChallengeProvider
		switch c.GlobalString("dns") {
		case "cloudflare":
			provider, err = cloudflare.NewDNSProvider()
		case "digitalocean":
			provider, err = digitalocean.NewDNSProvider()
		case "dnsimple":
			provider, err = dnsimple.NewDNSProvider()
		case "dyn":
			provider, err = dyn.NewDNSProvider()
		case "gandi":
			provider, err = gandi.NewDNSProvider()
		case "gcloud":
			provider, err = googlecloud.NewDNSProvider()
		case "manual":
			provider, err = acme.NewDNSProviderManual()
		case "namecheap":
			provider, err = namecheap.NewDNSProvider()
		case "route53":
			provider, err = route53.NewDNSProvider()
		case "rfc2136":
			provider, err = rfc2136.NewDNSProvider()
		case "vultr":
			provider, err = vultr.NewDNSProvider()
		case "ovh":
			provider, err = ovh.NewDNSProvider()
		}

		if err != nil {
			logger().Fatal(err)
		}

		client.SetChallengeProvider(acme.DNS01, provider)

		// --dns=foo indicates that the user specifically want to do a DNS challenge
		// infer that the user also wants to exclude all other challenges
		client.ExcludeChallenges([]acme.Challenge{acme.HTTP01, acme.TLSSNI01})
	}

	return conf, acc, client
}

func saveCertRes(certRes acme.CertificateResource, conf *Configuration) {
	// We store the certificate, private key and metadata in different files
	// as web servers would not be able to work with a combined file.
	certOut := path.Join(conf.CertPath(), certRes.Domain+".crt")
	privOut := path.Join(conf.CertPath(), certRes.Domain+".key")
	metaOut := path.Join(conf.CertPath(), certRes.Domain+".json")

	err := ioutil.WriteFile(certOut, certRes.Certificate, 0600)
	if err != nil {
		logger().Fatalf("Unable to save Certificate for domain %s\n\t%s", certRes.Domain, err.Error())
	}

	err = ioutil.WriteFile(privOut, certRes.PrivateKey, 0600)
	if err != nil {
		logger().Fatalf("Unable to save PrivateKey for domain %s\n\t%s", certRes.Domain, err.Error())
	}

	jsonBytes, err := json.MarshalIndent(certRes, "", "\t")
	if err != nil {
		logger().Fatalf("Unable to marshal CertResource for domain %s\n\t%s", certRes.Domain, err.Error())
	}

	err = ioutil.WriteFile(metaOut, jsonBytes, 0600)
	if err != nil {
		logger().Fatalf("Unable to save CertResource for domain %s\n\t%s", certRes.Domain, err.Error())
	}
}

func handleTOS(c *cli.Context, client *acme.Client, acc *Account) {
	// Check for a global accept override
	if c.GlobalBool("accept-tos") {
		err := client.AgreeToTOS()
		if err != nil {
			logger().Fatalf("Could not agree to TOS: %s", err.Error())
		}

		acc.Save()
		return
	}

	reader := bufio.NewReader(os.Stdin)
	logger().Printf("Please review the TOS at %s", acc.Registration.TosURL)

	for {
		logger().Println("Do you accept the TOS? Y/n")
		text, err := reader.ReadString('\n')
		if err != nil {
			logger().Fatalf("Could not read from console: %s", err.Error())
		}

		text = strings.Trim(text, "\r\n")

		if text == "n" {
			logger().Fatal("You did not accept the TOS. Unable to proceed.")
		}

		if text == "Y" || text == "y" || text == "" {
			err = client.AgreeToTOS()
			if err != nil {
				logger().Fatalf("Could not agree to TOS: %s", err.Error())
			}
			acc.Save()
			break
		}

		logger().Println("Your input was invalid. Please answer with one of Y/y, n or by pressing enter.")
	}
}

func run(c *cli.Context) error {
	conf, acc, client := setup(c)
	if acc.Registration == nil {
		reg, err := client.Register()
		if err != nil {
			logger().Fatalf("Could not complete registration\n\t%s", err.Error())
		}

		acc.Registration = reg
		acc.Save()

		logger().Print("!!!! HEADS UP !!!!")
		logger().Printf(`
		Your account credentials have been saved in your Let's Encrypt
		configuration directory at "%s".
		You should make a secure backup	of this folder now. This
		configuration directory will also contain certificates and
		private keys obtained from Let's Encrypt so making regular
		backups of this folder is ideal.`, conf.AccountPath(c.GlobalString("email")))

	}

	// If the agreement URL is empty, the account still needs to accept the LE TOS.
	if acc.Registration.Body.Agreement == "" {
		handleTOS(c, client, acc)
	}

	if len(c.GlobalStringSlice("domains")) == 0 {
		logger().Fatal("Please specify --domains or -d")
	}

	cert, failures := client.ObtainCertificate(c.GlobalStringSlice("domains"), !c.Bool("no-bundle"), nil)
	if len(failures) > 0 {
		for k, v := range failures {
			logger().Printf("[%s] Could not obtain certificates\n\t%s", k, v.Error())
		}

		// Make sure to return a non-zero exit code if ObtainSANCertificate
		// returned at least one error. Due to us not returning partial
		// certificate we can just exit here instead of at the end.
		os.Exit(1)
	}

	err := checkFolder(conf.CertPath())
	if err != nil {
		logger().Fatalf("Could not check/create path: %s", err.Error())
	}

	saveCertRes(cert, conf)

	return nil
}

func revoke(c *cli.Context) error {

	conf, _, client := setup(c)

	err := checkFolder(conf.CertPath())
	if err != nil {
		logger().Fatalf("Could not check/create path: %s", err.Error())
	}

	for _, domain := range c.GlobalStringSlice("domains") {
		logger().Printf("Trying to revoke certificate for domain %s", domain)

		certPath := path.Join(conf.CertPath(), domain+".crt")
		certBytes, err := ioutil.ReadFile(certPath)

		err = client.RevokeCertificate(certBytes)
		if err != nil {
			logger().Fatalf("Error while revoking the certificate for domain %s\n\t%s", domain, err.Error())
		} else {
			logger().Print("Certificate was revoked.")
		}
	}

	return nil
}

func renew(c *cli.Context) error {
	conf, _, client := setup(c)

	if len(c.GlobalStringSlice("domains")) <= 0 {
		logger().Fatal("Please specify at least one domain.")
	}

	domain := c.GlobalStringSlice("domains")[0]

	// load the cert resource from files.
	// We store the certificate, private key and metadata in different files
	// as web servers would not be able to work with a combined file.
	certPath := path.Join(conf.CertPath(), domain+".crt")
	privPath := path.Join(conf.CertPath(), domain+".key")
	metaPath := path.Join(conf.CertPath(), domain+".json")

	certBytes, err := ioutil.ReadFile(certPath)
	if err != nil {
		logger().Fatalf("Error while loading the certificate for domain %s\n\t%s", domain, err.Error())
	}

	if c.IsSet("days") {
		expTime, err := acme.GetPEMCertExpiration(certBytes)
		if err != nil {
			logger().Printf("Could not get Certification expiration for domain %s", domain)
		}

		if int(expTime.Sub(time.Now()).Hours()/24.0) > c.Int("days") {
			return nil
		}
	}

	metaBytes, err := ioutil.ReadFile(metaPath)
	if err != nil {
		logger().Fatalf("Error while loading the meta data for domain %s\n\t%s", domain, err.Error())
	}

	var certRes acme.CertificateResource
	err = json.Unmarshal(metaBytes, &certRes)
	if err != nil {
		logger().Fatalf("Error while marshalling the meta data for domain %s\n\t%s", domain, err.Error())
	}

	if c.Bool("reuse-key") {
		keyBytes, err := ioutil.ReadFile(privPath)
		if err != nil {
			logger().Fatalf("Error while loading the private key for domain %s\n\t%s", domain, err.Error())
		}
		certRes.PrivateKey = keyBytes
	}

	certRes.Certificate = certBytes

	newCert, err := client.RenewCertificate(certRes, !c.Bool("no-bundle"))
	if err != nil {
		logger().Fatalf("%s", err.Error())
	}

	saveCertRes(newCert, conf)

	return nil
}
