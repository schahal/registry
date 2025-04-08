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
	"sync"

	"sigs.k8s.io/yaml"
)

const rootRegistryPath = "./registry"

type directoryReadme struct {
	FilePath string
	RawText  string
}

type rawContributorProfileFrontmatter struct {
	DisplayName       string  `yaml:"display_name"`
	Bio               string  `yaml:"bio"`
	GithubUsername    string  `yaml:"github"`
	AvatarUrl         *string `yaml:"avatar"`
	LinkedinURL       *string `yaml:"linkedin"`
	WebsiteURL        *string `yaml:"website"`
	SupportEmail      *string `yaml:"support_email"`
	CompanyGithub     *string `yaml:"company_github"`
	ContributorStatus *string `yaml:"status"`
}

type trackableContributorFrontmatter struct {
	rawContributorProfileFrontmatter
	FilePath string
}

type contributorProfileStatus int

const (
	// Community should always be the first value defined via iota; it should be
	// treated as the zero value of the type in the event that a more specific
	// status wasn't defined
	profileStatusCommunity contributorProfileStatus = iota
	profileStatusPartner
	profileStatusOfficial
)

type contributorProfile struct {
	EmployeeGithubUsernames []string
	GithubUsername          string
	DisplayName             string
	Bio                     string
	AvatarUrl               string
	WebsiteURL              *string
	LinkedinURL             *string
	SupportEmail            *string
	Status                  contributorProfileStatus
}

var _ error = workflowPhaseError{}

type workflowPhaseError struct {
	Phase  string
	Errors []error
}

func (wpe workflowPhaseError) Error() string {
	msg := fmt.Sprintf("Error during phase %q of README validation:", wpe.Phase)
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
			fm += nextLine
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

func validateContributorYaml(yml trackableContributorFrontmatter) []error {
	// This function needs to aggregate a bunch of different errors, rather than
	// stopping at the first one found, so using code blocks to section off
	// logic for different fields
	errors := []error{}

	// GitHub Username
	{
		if yml.GithubUsername == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"missing GitHub username for %q",
					yml.FilePath,
				),
			)
		}

		lower := strings.ToLower(yml.GithubUsername)
		if uriSafe := url.PathEscape(lower); uriSafe != lower {
			errors = append(
				errors,
				fmt.Errorf(
					"gitHub username %q (%q) is not a valid URL path segment",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}
	}

	// Company GitHub
	if yml.CompanyGithub != nil {
		if *yml.CompanyGithub == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"company_github field is defined but has empty value for %q",
					yml.FilePath,
				),
			)
		}

		lower := strings.ToLower(*yml.CompanyGithub)
		if uriSafe := url.PathEscape(lower); uriSafe != lower {
			errors = append(
				errors,
				fmt.Errorf(
					"gitHub company username %q (%q) is not a valid URL path segment",
					*yml.CompanyGithub,
					yml.FilePath,
				),
			)
		}

		if *yml.CompanyGithub == yml.GithubUsername {
			errors = append(
				errors,
				fmt.Errorf(
					"cannot list own GitHub name (%q) as employer (%q)",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}
	}

	// Display name
	{
		if yml.DisplayName == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"%q (%q) is missing display name",
					yml.GithubUsername,
					yml.FilePath,
				),
			)
		}
	}

	// LinkedIn URL
	if yml.LinkedinURL != nil {
		if _, err := url.ParseRequestURI(*yml.LinkedinURL); err != nil {
			errors = append(
				errors,
				fmt.Errorf(
					"linkedIn URL %q (%q) is not valid: %v",
					*yml.LinkedinURL,
					yml.FilePath,
					err,
				),
			)
		}
	}

	// Email
	if yml.SupportEmail != nil {
		// Can't 100% validate that this is correct without actually sending
		// an email, and especially with some contributors being individual
		// developers, we don't want to do that on every single run of the CI
		// pipeline. Best we can do is verify the general structure
		username, server, ok := strings.Cut(*yml.SupportEmail, "@")
		if !ok {
			errors = append(
				errors,
				fmt.Errorf(
					"email address %q (%q) is missing @ symbol",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
			goto website
		}

		if username == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"email address %q (%q) is missing username",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}

		domain, tld, ok := strings.Cut(server, ".")
		if !ok {
			errors = append(
				errors,
				fmt.Errorf(
					"email address %q (%q) is missing period for server segment",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
			goto website
		}

		if domain == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"email address %q (%q) is missing domain",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}

		if tld == "" {
			errors = append(
				errors,
				fmt.Errorf(
					"email address %q (%q) is missing top-level domain",
					*yml.LinkedinURL,
					yml.FilePath,
				),
			)
		}
	}

	// Website
website:
	if yml.WebsiteURL != nil {
		if _, err := url.ParseRequestURI(*yml.WebsiteURL); err != nil {
			errors = append(
				errors,
				fmt.Errorf(
					"LinkedIn URL %q (%q) is not valid: %v",
					*yml.WebsiteURL,
					yml.FilePath,
					err,
				),
			)
		}
	}

	// Contributor status
	if yml.ContributorStatus != nil {
		validStatuses := []string{"official", "partner", "community"}
		if !slices.Contains(validStatuses, *yml.ContributorStatus) {
			errors = append(
				errors,
				fmt.Errorf(
					"contributor status %q (%q) is not valid",
					*yml.ContributorStatus,
					yml.FilePath,
				),
			)
		}
	}

	return errors
}

func remapContributorProfile(
	frontmatter trackableContributorFrontmatter,
	employeeGitHubNames []string,
) contributorProfile {
	// Function assumes that fields are previously validated and are safe to
	// copy over verbatim when appropriate
	remapped := contributorProfile{
		DisplayName:    frontmatter.DisplayName,
		GithubUsername: frontmatter.GithubUsername,
		Bio:            frontmatter.Bio,
		LinkedinURL:    frontmatter.LinkedinURL,
		SupportEmail:   frontmatter.SupportEmail,
	}

	if frontmatter.AvatarUrl != nil {
		remapped.AvatarUrl = *frontmatter.AvatarUrl
	}
	if frontmatter.ContributorStatus != nil {
		switch *frontmatter.ContributorStatus {
		case "partner":
			remapped.Status = profileStatusPartner
		case "official":
			remapped.Status = profileStatusOfficial
		default:
			remapped.Status = profileStatusCommunity
		}
	}
	if employeeGitHubNames != nil {
		remapped.EmployeeGithubUsernames = employeeGitHubNames[:]
		slices.Sort(remapped.EmployeeGithubUsernames)
	}

	return remapped
}

func parseContributorFiles(input []directoryReadme) (
	map[string]contributorProfile,
	error,
) {
	frontmatterByGithub := map[string]trackableContributorFrontmatter{}
	yamlParsingErrors := workflowPhaseError{
		Phase: "YAML parsing",
	}
	for _, dirReadme := range input {
		fmText, err := extractFrontmatter(dirReadme.RawText)
		if err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("failed to parse %q: %v", dirReadme.FilePath, err),
			)
			continue
		}

		yml := rawContributorProfileFrontmatter{}
		if err := yaml.Unmarshal([]byte(fmText), &yml); err != nil {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf("failed to parse %q: %v", dirReadme.FilePath, err),
			)
			continue
		}
		trackable := trackableContributorFrontmatter{
			FilePath:                         dirReadme.FilePath,
			rawContributorProfileFrontmatter: yml,
		}

		if prev, conflict := frontmatterByGithub[trackable.GithubUsername]; conflict {
			yamlParsingErrors.Errors = append(
				yamlParsingErrors.Errors,
				fmt.Errorf(
					"GitHub name conflict for %q for files %q and %q",
					trackable.GithubUsername,
					trackable.FilePath,
					prev.FilePath,
				),
			)
			continue
		}

		frontmatterByGithub[trackable.GithubUsername] = trackable
	}

	employeeGithubGroups := map[string][]string{}
	yamlValidationErrors := workflowPhaseError{
		Phase: "Raw YAML Validation",
	}
	for _, yml := range frontmatterByGithub {
		errors := validateContributorYaml(yml)
		if len(errors) > 0 {
			yamlValidationErrors.Errors = append(
				yamlValidationErrors.Errors,
				errors...,
			)
			continue
		}

		if yml.CompanyGithub != nil {
			employeeGithubGroups[*yml.CompanyGithub] = append(
				employeeGithubGroups[*yml.CompanyGithub],
				yml.GithubUsername,
			)
		}
	}
	if len(yamlValidationErrors.Errors) != 0 {
		return nil, yamlValidationErrors
	}

	contributorError := workflowPhaseError{
		Phase: "Contributor struct remapping",
	}
	structured := map[string]contributorProfile{}
	for _, yml := range frontmatterByGithub {
		group := employeeGithubGroups[yml.GithubUsername]
		remapped := remapContributorProfile(yml, group)
		structured[yml.GithubUsername] = remapped
	}
	for companyName, group := range employeeGithubGroups {
		if _, found := structured[companyName]; found {
			continue
		}
		contributorError.Errors = append(
			contributorError.Errors,
			fmt.Errorf(
				"company %q does not exist in %q directory but is referenced by these profiles: [%s]",
				rootRegistryPath,
				companyName,
				strings.Join(group, ", "),
			),
		)
	}
	if len(contributorError.Errors) != 0 {
		return nil, contributorError
	}

	return structured, nil
}

func backfillAvatarUrls(contributors map[string]contributorProfile) error {
	wg := sync.WaitGroup{}
	requestBuffer := make(chan struct{}, 10)
	errors := []error{}

	for _, c := range contributors {
		wg.Add(1)
		go func() {
			requestBuffer <- struct{}{}
			// Do request stuff

			<-requestBuffer
			wg.Done()
		}()
	}

	wg.Wait()
	if len(errors) == 0 {
		return nil
	}

	slices.SortFunc(errors, func(e1 error, e2 error) int {
		return strings.Compare(e1.Error(), e2.Error())
	})
	return workflowPhaseError{
		Phase:  "Avatar Backfill",
		Errors: errors,
	}
}

func main() {
	dirEntries, err := os.ReadDir(rootRegistryPath)
	if err != nil {
		log.Panic(err)
	}
	allReadmeFiles := []directoryReadme{}
	fsErrors := workflowPhaseError{
		Phase:  "FileSystem reading",
		Errors: []error{},
	}

	for _, e := range dirEntries {
		dirPath := path.Join(rootRegistryPath, e.Name())
		if !e.IsDir() {
			fsErrors.Errors = append(
				fsErrors.Errors,
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
			fsErrors.Errors = append(fsErrors.Errors, err)
			continue
		}
		allReadmeFiles = append(allReadmeFiles, directoryReadme{
			FilePath: readmePath,
			RawText:  string(rmBytes),
		})
	}
	if len(fsErrors.Errors) != 0 {
		log.Panic(fsErrors)
	}

	contributors, err := parseContributorFiles(allReadmeFiles)
	if err != nil {
		log.Panic(err)
	}
	err = backfillAvatarUrls(contributors)
	if err != nil {
		log.Panic(err)
	}

	log.Printf(
		"Processed all READMEs in the %q directory\n",
		rootRegistryPath,
	)
}
