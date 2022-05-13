package oras

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/distribution/distribution/v3"
	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/opencontainers/go-digest"
	orasartifacts "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// ReferrersResponse describes the response body of the referrers API.
type ReferrersResponse struct {
	Referrers []orasartifacts.Descriptor `json:"referrers"`
}

const maxPageSize = 50

// minimum page size used for # of digests to put in nextToken
const minPageSize = 3

func (h *referrersHandler) getReferrers(w http.ResponseWriter, r *http.Request) {
	dcontext.GetLogger(h.extContext).Debug("Get")

	// This can be empty
	artifactType := r.FormValue("artifactType")
	nPage, nParseError := strconv.Atoi(r.FormValue("n"))

	// client specified nPage must be greater than min page size and less than or equal to max page size
	if nParseError != nil || nPage < minPageSize || nPage > maxPageSize {
		nPage = maxPageSize
	}
	nextToken := r.FormValue("nextToken")
	nextTokenMap := make(map[string]string)
	if nextToken != "" {
		// base64 decode nextToken to string
		nextTokenDecoded, err := base64.RawURLEncoding.DecodeString(nextToken)
		if err != nil {
			h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeMalformedNextToken.WithDetail("nextToken base64 decoding failed"))
			return
		}
		nextTokenList := strings.Split(string(nextTokenDecoded), ",")
		for _, token := range nextTokenList {
			_, err := digest.Parse(token)
			if err != nil {
				h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeMalformedNextToken.WithDetail("nextToken parsing failed"))
				return
			}
			// store nextToken digest in a map for quick access
			nextTokenMap[token] = token
		}
	}

	if h.Digest == "" {
		h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeManifestUnknown.WithDetail("digest not specified"))
		return
	}

	referrers, err := h.Referrers(h.extContext, h.Digest, artifactType)
	if err != nil {
		if _, ok := err.(distribution.ErrManifestUnknownRevision); ok {
			h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeManifestUnknown.WithDetail(err))
		} else {
			h.extContext.Errors = append(h.extContext.Errors, errcode.ErrorCodeUnknown.WithDetail(err))
		}
		return
	}

	if referrers == nil {
		referrers = []orasartifacts.Descriptor{}
	}

	// only consider pagination if # of referrers is greater than page size
	if len(referrers) > nPage {
		startIndex := 0
		if len(nextTokenMap) > 0 {
			for i, ref := range referrers {
				// check if ref matches a digest in nextToken list
				if _, ok := nextTokenMap[ref.Digest.String()]; ok {
					// set the starting index to the largest index
					if (i + 1) > startIndex {
						startIndex = i + 1
					}
				}
			}
		}

		// only applicable if last item provided as nextToken
		if startIndex == len(referrers) {
			referrers = []orasartifacts.Descriptor{}
		}

		// if there's only 1 page of results left
		if len(referrers)-startIndex <= nPage {
			referrers = referrers[startIndex:]
		} else {
			referrers = referrers[startIndex:(startIndex + nPage)]
			// nextToken is a base64 encoded comma-seperated string of the digests of the last three referrers in the response
			var nextDgsts []string
			for i := nPage - 1; i >= nPage-minPageSize; i-- {
				nextDgsts = append(nextDgsts, referrers[i].Digest.String())
			}
			// if n was not provided in page, set nPage to a value so link header knows not to include n
			if nParseError != nil {
				nPage = -1
			}
			// add the Link Header
			w.Header().Set("Link", generateLinkHeader(h.extContext.Repository.Named().Name(), h.Digest.String(), artifactType, nextDgsts, nPage))
		}
	}

	response := ReferrersResponse{
		Referrers: referrers,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ORAS-Api-Version", "oras/1.0")
	enc := json.NewEncoder(w)
	if err = enc.Encode(response); err != nil {
		h.extContext.Errors = append(h.extContext.Errors, errcode.ErrorCodeUnknown.WithDetail(err))
		return
	}
}

func generateLinkHeader(repoName, subjectDigest, artifactType string, lastDigests []string, nPage int) string {
	url := fmt.Sprintf("/v2/%s/_oras/artifacts/referrers?digest=%s&nextToken=%s",
		repoName,
		subjectDigest,
		base64.RawURLEncoding.EncodeToString([]byte(strings.Join(lastDigests, ","))))
	if artifactType != "" {
		url = fmt.Sprintf("%s&artifactType=%s", url, artifactType)
	}
	if nPage > 0 {
		url = fmt.Sprintf("%s&n=%d", url, nPage)
	}
	return fmt.Sprintf("<%s>; rel=\"next\"", url)
}
