// Generate the Windows icon and manifest resource without a platform SDK.
package main

import (
	"log"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	if err := generate(); err != nil {
		log.Fatal(err)
	}
}

func generate() error {
	file, err := os.Open("icons/app.ico")
	if err != nil {
		return err
	}
	defer file.Close()
	icon, err := winres.LoadICO(file)
	if err != nil {
		return err
	}
	resources := winres.ResourceSet{}
	// Wails loads application icons from resource ID 3.
	if err := resources.SetIcon(winres.RT_ICON, icon); err != nil {
		return err
	}
	manifest, err := os.ReadFile("icons/app.manifest")
	if err != nil {
		return err
	}
	xml, err := winres.AppManifestFromXML(manifest)
	if err != nil {
		return err
	}
	resources.SetManifest(xml)
	output, err := os.Create("rsrc_windows_amd64.syso")
	if err != nil {
		return err
	}
	if err := resources.WriteObject(output, winres.ArchAMD64); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
