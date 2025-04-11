// This package is for validating all contributors within the main Registry
// directory. It validates that it has nothing but sub-directories, and that
// each sub-directory has a README.md file. Each of those files must then
// describe a specific contributor. The contents of these files will be parsed
// by the Registry site build step, to be displayed in the Registry site's UI.
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

type readme struct {
	FilePath string
	RawText  string
}

type contributorProfileFrontmatter struct {
	DisplayName    string `yaml:"display_name"`
	Bio            string `yaml:"bio"`
	GithubUsername string `yaml:"github"`
	// Script assumes that if value is nil, the Registry site build step will
	// backfill the value with the user's GitHub avatar URL
	AvatarURL              *string `yaml:"avatar"`
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

var _ error = validationPhaseError{}

type validationPhaseError struct {
	Phase  string
	Errors []error
}

func (vpe validationPhaseError) Error() string {
	msg := fmt.Sprintf("Error during %q phase of README validation:", vpe.Phase)
	for _, e := range vpe.Errors {
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

// A validation function for verifying one specific aspect of a contributor's
// frontmatter content. Each function should be able to return ALL data
// violations that apply to the function's area of concern, rather than
// returning the first error found
type contributorValidationFunc = func(fm contributorFrontmatterWithFilePath) []error

func validateContributorGithubUsername(fm contributorFrontmatterWithFilePath) []error {
	problems := []error{}

	if fm.GithubUsername == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: missing GitHub username",
				fm.FilePath,
			),
		)
		return problems
	}

	lower := strings.ToLower(fm.GithubUsername)
	if uriSafe := url.PathEscape(lower); uriSafe != lower {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: gitHub username %q is not a valid URL path segment",
				fm.FilePath,
				fm.GithubUsername,
			),
		)
	}

	return problems
}

func validateContributorEmployerGithubUsername(fm contributorFrontmatterWithFilePath) []error {
	if fm.EmployerGithubUsername == nil {
		return nil
	}

	problems := []error{}

	if *fm.EmployerGithubUsername == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: company_github field is defined but has empty value",
				fm.FilePath,
			),
		)
		return problems
	}

	lower := strings.ToLower(*fm.EmployerGithubUsername)
	if uriSafe := url.PathEscape(lower); uriSafe != lower {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: gitHub company username %q is not a valid URL path segment",
				fm.FilePath,
				*fm.EmployerGithubUsername,
			),
		)
	}

	if *fm.EmployerGithubUsername == fm.GithubUsername {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: cannot list own GitHub name (%q) as employer",
				fm.FilePath,
				fm.GithubUsername,
			),
		)
	}

	return problems
}

func validateContributorDisplayName(fm contributorFrontmatterWithFilePath) []error {
	problems := []error{}
	if fm.DisplayName == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: GitHub user %q is missing display name",
				fm.FilePath,
				fm.GithubUsername,
			),
		)
	}

	return problems
}

func validateContributorLinkedinURL(fm contributorFrontmatterWithFilePath) []error {
	if fm.LinkedinURL == nil {
		return nil
	}

	problems := []error{}
	if _, err := url.ParseRequestURI(*fm.LinkedinURL); err != nil {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: linkedIn URL %q is not valid: %v",
				*fm.LinkedinURL,
				fm.FilePath,
				err,
			),
		)
	}

	return problems
}

func validateContributorEmail(fm contributorFrontmatterWithFilePath) []error {
	if fm.SupportEmail == nil {
		return nil
	}

	problems := []error{}

	// Can't 100% validate that this is correct without actually sending
	// an email, and especially with some contributors being individual
	// developers, we don't want to do that on every single run of the CI
	// pipeline. Best we can do is verify the general structure
	username, server, ok := strings.Cut(*fm.SupportEmail, "@")
	if !ok {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email address %q is missing @ symbol",
				fm.FilePath,
				*fm.LinkedinURL,
			),
		)
		return problems
	}

	if username == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email address %q is missing username",
				fm.FilePath,
				*fm.LinkedinURL,
			),
		)
	}

	domain, tld, ok := strings.Cut(server, ".")
	if !ok {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email address %q is missing period for server segment",
				fm.FilePath,
				*fm.LinkedinURL,
			),
		)
		return problems
	}

	if domain == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email address %q is missing domain",
				fm.FilePath,
				*fm.LinkedinURL,
			),
		)
	}

	if tld == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email address %q is missing top-level domain",
				fm.FilePath,
				*fm.LinkedinURL,
			),
		)
	}

	if strings.Contains(*fm.SupportEmail, "?") {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: email is not allowed to contain search parameters",
				fm.FilePath,
			),
		)
	}

	return problems
}

func validateContributorWebsite(fm contributorFrontmatterWithFilePath) []error {
	if fm.WebsiteURL == nil {
		return nil
	}

	problems := []error{}
	if _, err := url.ParseRequestURI(*fm.WebsiteURL); err != nil {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: LinkedIn URL %q is not valid: %v",
				fm.FilePath,
				*fm.WebsiteURL,
				err,
			),
		)
	}

	return problems
}

func validateContributorStatus(fm contributorFrontmatterWithFilePath) []error {
	if fm.ContributorStatus == nil {
		return nil
	}

	problems := []error{}
	validStatuses := []string{"official", "partner", "community"}
	if !slices.Contains(validStatuses, *fm.ContributorStatus) {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: contributor status %q is not valid",
				fm.FilePath,
				*fm.ContributorStatus,
			),
		)
	}

	return problems
}

// Can't validate the image actually leads to a valid resource in a pure
// function, but can at least catch obvious problems
func validateContributorAvatarURL(fm contributorFrontmatterWithFilePath) []error {
	if fm.AvatarURL == nil {
		return nil
	}

	problems := []error{}
	if *fm.AvatarURL == "" {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: avatar URL must be omitted or non-empty string",
				fm.FilePath,
			),
		)
		return problems
	}

	// Have to use .Parse instead of .ParseRequestURI because this is the
	// one field that's allowed to be a relative URL
	if _, err := url.Parse(*fm.AvatarURL); err != nil {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: URL %q is not a valid relative or absolute URL",
				fm.FilePath,
				*fm.AvatarURL,
			),
		)
	}

	if strings.Contains(*fm.AvatarURL, "?") {
		problems = append(
			problems,
			fmt.Errorf(
				"%q: avatar URL is not allowed to contain search parameters",
				fm.FilePath,
			),
		)
	}

	supportedFileFormats := []string{".png", ".jpeg", ".jpg", ".gif", ".svg"}
	matched := false
	for _, ff := range supportedFileFormats {
		matched = strings.HasSuffix(*fm.AvatarURL, ff)
		if matched {
			break
		}
	}
	if !matched {
		segments := strings.Split(*fm.AvatarURL, ".")
		fileExtension := segments[len(segments)-1]
		problems = append(
			problems,
			fmt.Errorf(
				"%q: avatar URL '.%s' does not end in a supported file format: [%s]",
				fm.FilePath,
				fileExtension,
				strings.Join(supportedFileFormats, ", "),
			),
		)
	}

	return problems
}

func validateContributorYaml(yml contributorFrontmatterWithFilePath) []error {
	validationFuncs := []contributorValidationFunc{
		validateContributorGithubUsername,
		validateContributorEmployerGithubUsername,
		validateContributorDisplayName,
		validateContributorLinkedinURL,
		validateContributorEmail,
		validateContributorWebsite,
		validateContributorStatus,
		validateContributorAvatarURL,
	}
	allProblems := []error{}
	for _, fn := range validationFuncs {
		allProblems = append(allProblems, fn(yml)...)
	}
	return allProblems
}

func parseContributorFiles(readmeEntries []readme) (
	map[string]contributorFrontmatterWithFilePath,
	error,
) {
	frontmatterByUsername := map[string]contributorFrontmatterWithFilePath{}
	yamlParsingErrors := validationPhaseError{
		Phase: "YAML parsing",
	}
	for _, rm := range readmeEntries {
		fm, err := extractFrontmatter(rm.RawText)
		if err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("%q: failed to parse: %v", rm.FilePath, err),
			)
			continue
		}

		yml := contributorProfileFrontmatter{}
		if err := yaml.Unmarshal([]byte(fm), &yml); err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("%q: failed to parse: %v", rm.FilePath, err),
			)
			continue
		}
		processed := contributorFrontmatterWithFilePath{
			FilePath:                      rm.FilePath,
			contributorProfileFrontmatter: yml,
		}

		if prev, isConflict := frontmatterByUsername[processed.GithubUsername]; isConflict {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf(
					"%q: GitHub name %s conflicts with field defined in %q",
					processed.FilePath,
					processed.GithubUsername,
					prev.FilePath,
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
	yamlValidationErrors := validationPhaseError{
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

func aggregateContributorReadmeFiles() ([]readme, error) {
	dirEntries, err := os.ReadDir(rootRegistryPath)
	if err != nil {
		return nil, err
	}

	allReadmeFiles := []readme{}
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
		allReadmeFiles = append(allReadmeFiles, readme{
			FilePath: readmePath,
			RawText:  string(rmBytes),
		})
	}

	if len(problems) != 0 {
		return nil, validationPhaseError{
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
		if con.AvatarURL == nil {
			continue
		}
		if isRelativeURL := strings.HasPrefix(*con.AvatarURL, ".") ||
			strings.HasPrefix(*con.AvatarURL, "/"); !isRelativeURL {
			continue
		}

		if strings.HasPrefix(*con.AvatarURL, "..") {
			problems = append(
				problems,
				fmt.Errorf(
					"%q: relative avatar URLs cannot be placed outside a user's namespaced directory",
					con.FilePath,
				),
			)
			continue
		}

		absolutePath := strings.TrimSuffix(con.FilePath, "README.md") +
			*con.AvatarURL
		_, err := os.ReadFile(absolutePath)
		if err != nil {
			problems = append(
				problems,
				fmt.Errorf(
					"%q: relative avatar path %q does not point to image in file system",
					con.FilePath,
					*con.AvatarURL,
				),
			)
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return validationPhaseError{
		Phase:  "Relative URL validation",
		Errors: problems,
	}
}

func main() {
	log.Println("Starting README validation")
	allReadmeFiles, err := aggregateContributorReadmeFiles()
	if err != nil {
		log.Panic(err)
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
