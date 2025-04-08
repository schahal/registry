package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const rootRegistryPath = "./registry"

type directoryReadme struct {
	FilePath string
	RawText  string
}

type contributorProfileFrontmatter struct {
	DisplayName            string  `yaml:"display_name"`
	Bio                    string  `yaml:"bio"`
	GithubUsername         string  `yaml:"github"`
	AvatarUrl              *string `yaml:"avatar"` // Script assumes that if value is nil, the Registry site build step will backfill the value with the user's GitHub avatar URL
	LinkedinURL            *string `yaml:"linkedin"`
	WebsiteURL             *string `yaml:"website"`
	SupportEmail           *string `yaml:"support_email"`
	EmployerGithubUsername *string `yaml:"employer_github"`
	ContributorStatus      *string `yaml:"status"`
}

type contributorFrontmatterWithFilePath struct {
	contributorProfileFrontmatter
	FilePath string
}

var _ error = workflowPhaseError{}

type workflowPhaseError struct {
	Phase  string
	Errors []error
}

func (wpe workflowPhaseError) Error() string {
	msg := fmt.Sprintf("Error during %q phase of README validation:", wpe.Phase)
	for _, e := range wpe.Errors {
		msg += fmt.Sprintf("\n- %v", e)
	}
	msg += "\n"

	return msg
}

func extractFrontmatter(readmeText string) (string, error) {
	if readmeText == "" {
		return "", errors.New("README is empty")
	}

	const fence = "---"
	fm := ""
	fenceCount := 0
	lineScanner := bufio.NewScanner(
		strings.NewReader(strings.TrimSpace(readmeText)),
	)
	for lineScanner.Scan() {
		nextLine := lineScanner.Text()
		if fenceCount == 0 && nextLine != fence {
			return "", errors.New("README does not start with frontmatter fence")
		}

		if nextLine != fence {
			fm += nextLine + "\n"
			continue
		}

		fenceCount++
		if fenceCount >= 2 {
			break
		}
	}

	if fenceCount == 1 {
		return "", errors.New("README does not have two sets of frontmatter fences")
	}
	return fm, nil
}

func validateContributorYaml(yml contributorFrontmatterWithFilePath) []error {
	// This function needs to aggregate a bunch of different problems, rather
	// than stopping at the first one found, so using code blocks to section off
	// logic for different fields
	problems := []error{}

	// Using a bunch of closures to group validations for each field and add
	// support for ending validations for a group early. The alternatives were
	// making a bunch of functions in the top-level that would only be used
	// once, or using goto statements, which would've made refactoring fragile

	// GitHub Username
	func() {
		if yml.GithubUsername == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"missing GitHub username for %q",
					yml.FilePath,
				),
			)
			return
		}

		lower := strings.ToLower(yml.GithubUsername)
		if uriSafe := url.PathEscape(lower); uriSafe != lower {
			problems = append(
				problems,
				fmt.Errorf(
					"gitHub username %q (%q) is not a valid URL path segment",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}
	}()

	// Company GitHub
	func() {
		if yml.EmployerGithubUsername == nil {
			return
		}

		if *yml.EmployerGithubUsername == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"company_github field is defined but has empty value for %q",
					yml.FilePath,
				),
			)
			return
		}

		lower := strings.ToLower(*yml.EmployerGithubUsername)
		if uriSafe := url.PathEscape(lower); uriSafe != lower {
			problems = append(
				problems,
				fmt.Errorf(
					"gitHub company username %q (%q) is not a valid URL path segment",
					*yml.EmployerGithubUsername,
					yml.FilePath,
				),
			)
		}

		if *yml.EmployerGithubUsername == yml.GithubUsername {
			problems = append(
				problems,
				fmt.Errorf(
					"cannot list own GitHub name (%q) as employer (%q)",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}
	}()

	// Display name
	func() {
		if yml.DisplayName == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"GitHub user %q (%q) is missing display name",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}

	}()

	// LinkedIn URL
	func() {
		if yml.LinkedinURL == nil {
			return
		}

		if _, err := url.ParseRequestURI(*yml.LinkedinURL); err != nil {
			problems = append(
				problems,
				fmt.Errorf(
					"linkedIn URL %q (%q) is not valid: %v",
					*yml.LinkedinURL,
					yml.FilePath,
					err,
				),
			)
		}
	}()

	// Email
	func() {
		if yml.SupportEmail == nil {
			return
		}

		// Can't 100% validate that this is correct without actually sending
		// an email, and especially with some contributors being individual
		// developers, we don't want to do that on every single run of the CI
		// pipeline. Best we can do is verify the general structure
		username, server, ok := strings.Cut(*yml.SupportEmail, "@")
		if !ok {
			problems = append(
				problems,
				fmt.Errorf(
					"email address %q (%q) is missing @ symbol",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
			return
		}

		if username == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"email address %q (%q) is missing username",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}

		domain, tld, ok := strings.Cut(server, ".")
		if !ok {
			problems = append(
				problems,
				fmt.Errorf(
					"email address %q (%q) is missing period for server segment",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
			return
		}

		if domain == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"email address %q (%q) is missing domain",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}

		if tld == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"email address %q (%q) is missing top-level domain",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}

		if strings.Contains(*yml.SupportEmail, "?") {
			problems = append(
				problems,
				fmt.Errorf(
					"email for %q is not allowed to contain search parameters",
					yml.FilePath,
				),
			)
		}
	}()

	// Website
	func() {
		if yml.WebsiteURL == nil {
			return
		}

		if _, err := url.ParseRequestURI(*yml.WebsiteURL); err != nil {
			problems = append(
				problems,
				fmt.Errorf(
					"LinkedIn URL %q (%q) is not valid: %v",
					*yml.WebsiteURL,
					yml.FilePath,
					err,
				),
			)
		}
	}()

	// Contributor status
	func() {
		if yml.ContributorStatus == nil {
			return
		}

		validStatuses := []string{"official", "partner", "community"}
		if !slices.Contains(validStatuses, *yml.ContributorStatus) {
			problems = append(
				problems,
				fmt.Errorf(
					"contributor status %q (%q) is not valid",
					*yml.ContributorStatus,
					yml.FilePath,
				),
			)
		}
	}()

	// Avatar URL - can't validate the image actually leads to a valid resource
	// in a pure function, but can at least catch obvious problems
	func() {
		if yml.AvatarUrl == nil {
			return
		}

		if *yml.AvatarUrl == "" {
			problems = append(
				problems,
				fmt.Errorf(
					"avatar URL for %q must be omitted or non-empty string",
					yml.FilePath,
				),
			)
			return
		}

		// Have to use .Parse instead of .ParseRequestURI because this is the
		// one field that's allowed to be a relative URL
		if _, err := url.Parse(*yml.AvatarUrl); err != nil {
			problems = append(
				problems,
				fmt.Errorf(
					"error %q (%q) is not a valid relative or absolute URL",
					*yml.AvatarUrl,
					yml.FilePath,
				),
			)
		}

		if strings.Contains(*yml.AvatarUrl, "?") {
			problems = append(
				problems,
				fmt.Errorf(
					"avatar URL for %q is not allowed to contain search parameters",
					yml.FilePath,
				),
			)
		}
	}()

	return problems
}

func parseContributorFiles(readmeEntries []directoryReadme) (
	map[string]contributorFrontmatterWithFilePath,
	error,
) {
	frontmatterByUsername := map[string]contributorFrontmatterWithFilePath{}
	yamlParsingErrors := workflowPhaseError{
		Phase: "YAML parsing",
	}
	for _, rm := range readmeEntries {
		fm, err := extractFrontmatter(rm.RawText)
		if err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("failed to parse %q: %v", rm.FilePath, err),
			)
			continue
		}

		yml := contributorProfileFrontmatter{}
		if err := yaml.Unmarshal([]byte(fm), &yml); err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("failed to parse %q: %v", rm.FilePath, err),
			)
			continue
		}
		processed := contributorFrontmatterWithFilePath{
			FilePath:                      rm.FilePath,
			contributorProfileFrontmatter: yml,
		}

		if prev, conflict := frontmatterByUsername[processed.GithubUsername]; conflict {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf(
					"GitHub name conflict for %q for files %q and %q",
					processed.GithubUsername,
					prev.FilePath,
					processed.FilePath,
				),
			)
			continue
		}

		frontmatterByUsername[processed.GithubUsername] = processed
	}
	if len(yamlParsingErrors.Errors) != 0 {
		return nil, yamlParsingErrors
	}

	employeeGithubGroups := map[string][]string{}
	yamlValidationErrors := workflowPhaseError{
		Phase: "Raw YAML Validation",
	}
	for _, yml := range frontmatterByUsername {
		errors := validateContributorYaml(yml)
		if len(errors) > 0 {
			yamlValidationErrors.Errors = append(
				yamlValidationErrors.Errors,
				errors...,
			)
			continue
		}

		if yml.EmployerGithubUsername != nil {
			employeeGithubGroups[*yml.EmployerGithubUsername] = append(
				employeeGithubGroups[*yml.EmployerGithubUsername],
				yml.GithubUsername,
			)
		}
	}
	for companyName, group := range employeeGithubGroups {
		if _, found := frontmatterByUsername[companyName]; found {
			continue
		}
		yamlValidationErrors.Errors = append(
			yamlValidationErrors.Errors,
			fmt.Errorf(
				"company %q does not exist in %q directory but is referenced by these profiles: [%s]",
				companyName,
				rootRegistryPath,
				strings.Join(group, ", "),
			),
		)
	}
	if len(yamlValidationErrors.Errors) != 0 {
		return nil, yamlValidationErrors
	}

	return frontmatterByUsername, nil
}

func aggregateReadmeFiles() ([]directoryReadme, error) {
	dirEntries, err := os.ReadDir(rootRegistryPath)
	if err != nil {
		return nil, err
	}

	allReadmeFiles := []directoryReadme{}
	problems := []error{}
	for _, e := range dirEntries {
		dirPath := path.Join(rootRegistryPath, e.Name())
		if !e.IsDir() {
			problems = append(
				problems,
				fmt.Errorf(
					"Detected non-directory file %q at base of main Registry directory",
					dirPath,
				),
			)
			continue
		}

		readmePath := path.Join(dirPath, "README.md")
		rmBytes, err := os.ReadFile(readmePath)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		allReadmeFiles = append(allReadmeFiles, directoryReadme{
			FilePath: readmePath,
			RawText:  string(rmBytes),
		})
	}

	if len(problems) != 0 {
		return nil, workflowPhaseError{
			Phase:  "FileSystem reading",
			Errors: problems,
		}
	}

	return allReadmeFiles, nil
}

func validateRelativeUrls(
	contributors map[string]contributorFrontmatterWithFilePath,
) error {
	// This function only validates relative avatar URLs for now, but it can be
	// beefed up to validate more in the future
	problems := []error{}

	for _, con := range contributors {
		if con.AvatarUrl == nil {
			continue
		}
		if isRelativeUrl := strings.HasPrefix(*con.AvatarUrl, ".") ||
			strings.HasPrefix(*con.AvatarUrl, "/"); !isRelativeUrl {
			continue
		}

		fmt.Println(con.GithubUsername, con.FilePath, con.AvatarUrl)
	}

	if len(problems) == 0 {
		return nil
	}
	return workflowPhaseError{
		Phase:  "Relative URL validation",
		Errors: problems,
	}
}

func main() {
	log.Println("Starting README validation")
	allReadmeFiles, err := aggregateReadmeFiles()
	if err != nil {
		panic(err)
	}

	log.Printf("Processing %d README files\n", len(allReadmeFiles))
	contributors, err := parseContributorFiles(allReadmeFiles)
	if err != nil {
		log.Panic(err)
	}
	log.Printf(
		"Processed %d README files as valid contributor profiles",
		len(contributors),
	)

	err = validateRelativeUrls(contributors)
	if err != nil {
		log.Panic(err)
	}
	log.Println("All relative URLs for READMEs are valid")

	log.Printf(
		"Processed all READMEs in the %q directory\n",
		rootRegistryPath,
	)
}
