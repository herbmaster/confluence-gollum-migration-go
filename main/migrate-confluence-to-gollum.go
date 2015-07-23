package main

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"bytes"
	"os/exec"
	"encoding/binary"
	"io"
	"strings"
)

func html2markdown(html []byte) (markdown []byte, err error) {
	cmd := exec.Command("pandoc", "-f", "html", "-t", "markdown_strict+table_captions+pipe_tables", "--atx-headers")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	
	err = cmd.Start()
	if err != nil {
		return
	}
	stdin.Write([]byte(html))
	stdin.Close()
	
	markdown, err = ioutil.ReadAll(stdout)
	if err != nil {
		return
	}
	
	err = cmd.Wait()
	if err != nil {
		return
	}
	
	return
}

type Property struct {
	XMLName xml.Name `xml:"property"`
	Name string `xml:"name,attr"`
	Value []byte `xml:",chardata"`
}

type Id struct {
	XMLName xml.Name `xml:"id"`
	Name string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

type Element struct {
	XMLName xml.Name `xml:"element"`
	Class string `xml:"class,attr"`
	Package string `xml:"package,attr"`
	Id string `xml:"id"`
}

// <collection name="bodyContents" class="java.util.Collection">
// <element class="BodyContent" package="com.atlassian.confluence.core"><id name="id">2523176</id></element>
// </collection>

type Collection struct {
	XMLName xml.Name `xml:"collection"`
	Name string `xml:"name,attr"`
	Class string `xml:"class,attr"`
	Elements []Element  `xml:"element"`
}

type Object struct {
	XMLName xml.Name `xml:"object"`
	Id string `xml:"id"`
	Class string `xml:"class,attr"`
	Package string `xml:"package,attr"`
	Properties []Property `xml:"property"`
	Collections []Collection  `xml:"collection"`
}

type Result struct {
	XMLName xml.Name `xml:"hibernate-generic"`
	Objects   []Object `xml:"object"`
}


type ConfluencePage struct {
	Id string
	Title string
	BodyId string
	Version uint64
	Attachments []Element
}

type ConfluenceBodyContent struct {
	Id string
	Body []byte
}

type ConfluenceAttachment struct {
	Id string
	Title string
	Version uint64
}

type WikiPage struct {
	Title string
	Filename string
	Content string
	Path string
}

// copyFileContents copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file.
func copyFileContents(src, dst string) (err error) {
    in, err := os.Open(src)
    if err != nil {
        return
    }
    defer in.Close()
    out, err := os.Create(dst)
    if err != nil {
        return
    }
    defer func() {
        cerr := out.Close()
        if err == nil {
            err = cerr
        }
    }()
    if _, err = io.Copy(out, in); err != nil {
        return
    }
    err = out.Sync()
    return
}

func toAscii(str string) (result string) {
	result = str
	result = strings.Replace(result, "ä", "ae", -1)
	result = strings.Replace(result, "ö", "oe", -1)
	result = strings.Replace(result, "ü", "ue", -1)
	return
}

func slugify(str string) (result string) {
	result = toAscii(str)
	slugMatch := regexp.MustCompile(`[^A-Z0-9a-z]+`)
	result = slugMatch.ReplaceAllString(result, " ")
	result = strings.Title(result)
	result = strings.Replace(result, " ", "", -1)
	return
}

func main() {
	outputDir := os.Args[2]
	exportDir := os.Args[1]
	entitiesFile, err := os.Open(exportDir + "entities.xml")
	if err != nil {
		fmt.Printf("error: %v", err)
		os.Exit(1)
	}
	data, err := ioutil.ReadAll(entitiesFile)
	if err != nil {
		fmt.Printf("error: %v", err)
		os.Exit(1)
	}
	
	v := Result{}
	err = xml.Unmarshal([]byte(data), &v)
	if err != nil {
		fmt.Printf("error: %v", err)
		os.Exit(1)
	}
	
	confluencePages := make(map[string]ConfluencePage, 0)
	confluenceBodyContents := make(map[string]ConfluenceBodyContent, 0)
	confluenceAttachments := make(map[string]ConfluenceAttachment, 0)
	
	for _, obj := range v.Objects {
		// Find all pages
		if obj.Class == "Page" && obj.Package == "com.atlassian.confluence.pages" {
			p := ConfluencePage{}
			p.Id = obj.Id
			for _, prop := range obj.Properties {
				if prop.Name == "version" {
					p.Version, _ = binary.Uvarint(prop.Value)
				}
				if prop.Name == "title"  {
					p.Title = string(prop.Value)
				}
			}
			for _, coll := range obj.Collections {
				if coll.Name == "bodyContents" {
					p.BodyId = coll.Elements[0].Id
				}
				if coll.Name == "attachments" {
					p.Attachments = coll.Elements
				}
			}
			if previousP, ok := confluencePages[p.Title]; ok {
			    if (p.Version > previousP.Version) {
					confluencePages[p.Title] = p
				}
			} else {
				confluencePages[p.Title] = p
			}
		}
		// Find body contents
		if obj.Class == "BodyContent" && obj.Package == "com.atlassian.confluence.core" {
			b := ConfluenceBodyContent{}
			b.Id = obj.Id
			for _, prop := range obj.Properties {
				if prop.Name == "body" {
					b.Body = prop.Value
				}
			} 
			confluenceBodyContents[obj.Id] = b
		}
		// Find attachments
		if obj.Class == "Attachment" && obj.Package == "com.atlassian.confluence.pages" {
			a := ConfluenceAttachment{}
			a.Id = obj.Id
			for _, prop := range obj.Properties {
				if prop.Name == "title" {
					a.Title = string(prop.Value)
				}
				if prop.Name == "version" {
					a.Version, _ = binary.Uvarint(prop.Value)
				}
			}
			if previousA, ok := confluenceAttachments[obj.Id]; ok {
			    if (a.Version > previousA.Version) {
					confluenceAttachments[obj.Id] = a
				}
			} else {
				confluenceAttachments[obj.Id] = a
			}
		}
	}
	
	// Write to disk
	imageMatch := regexp.MustCompile(`<ac:image><ri:attachment ri:filename="([^"]+)" /></ac:image>`)
	tagsMatch := regexp.MustCompile(`</?[a-z]+((?m)[a-z]+="[^"]+")?>`)
	attrMatch := regexp.MustCompile(`<(span)( [a-z]+="[^"]+")?>`)

	for _, page := range confluencePages {
		w := WikiPage{}
		w.Path = "/"
		w.Filename = slugify(page.Title)
		fmt.Printf("%v\n", w.Filename)
		bodyContent := confluenceBodyContents[page.BodyId]
		html := bodyContent.Body
		for _, match := range imageMatch.FindAllSubmatch(html, -1) {
			// Create dir for attachmens
			err = os.Mkdir(outputDir + w.Path + w.Filename, 0755)
			if err != nil && !os.IsExist(err) {
				fmt.Printf("%v\n", err)
				return
			}

			// Find attachment with this name for this page
			for _, att := range page.Attachments {
				attachment := confluenceAttachments[att.Id]
				if attachment.Title == string(match[1]) {
					html = bytes.Replace(html, match[0], []byte(`<img src="` + w.Path + attachment.Title + `" alt="`+ attachment.Title +`">`), -1)
					// Copy attachment file
					attFile := exportDir + "attachments/" + page.Id + "/" + attachment.Id + "/" + string(attachment.Version)
					outFile := outputDir + w.Path + w.Filename + "/" + attachment.Title
					err = copyFileContents(attFile, outFile)
					if err != nil {
						fmt.Printf("%v\n", err)
						return
					}
				}
			}
		}
		
		// Preprocess HTML
		// _ remove html attributes from some elements
		html = attrMatch.ReplaceAll(html, []byte("<$1>"))	
		
		markdown, err := html2markdown(html)	
		if err != nil {
			fmt.Printf("%v\n", err)
			return
		}
		
		// Strip leftover tags
		markdown = tagsMatch.ReplaceAll(markdown, []byte(""))

		ioutil.WriteFile(outputDir + w.Filename + ".md", markdown, 0644)
		
	}
}