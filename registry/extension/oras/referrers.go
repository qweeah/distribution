package oras

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/distribution/distribution/v3"
	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/opencontainers/go-digest"
	orasartifacts "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// referrersResponse describes the response body of the referrers API.
type referrersResponse struct {
	Referrers []orasartifacts.Descriptor `json:"referrers"`
}

const maxPageSize = 50

func (h *referrersHandler) getReferrers(w http.ResponseWriter, r *http.Request) {
	dcontext.GetLogger(h.extContext).Debug("Get")

	// This can be empty
	artifactType := r.FormValue("artifactType")
	nPage, err := strconv.Atoi(r.FormValue("n"))
	if nPage < 0 || err != nil {
		nPage = maxPageSize
	}
	nextToken := r.FormValue("nextToken")
	if nextToken != "" {
		_, err = digest.Parse(nextToken)
		if err != nil {
			h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeDigestInvalid.WithDetail("nextToken digest parsing failed"))
			return
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
		if nextToken != "" {
			for i, ref := range referrers {
				if ref.Digest.String() == nextToken {
					startIndex = i + 1
				}
			}
			// if matching referrer not found
			if startIndex == 0 {
				h.extContext.Errors = append(h.extContext.Errors, v2.ErrorCodeReferrerNotFound.WithDetail("matching referrer with digest in nextToken not found"))
				return
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
			// add the Link Header
			w.Header().Set("Link", generateLinkHeader(h.extContext.Repository.Named().Name(), h.Digest.String(), artifactType, referrers[nPage-1].Digest.String(), nPage))
		}
	}

	response := referrersResponse{
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

func generateLinkHeader(repoName, subjectDigest, artifactType, lastDigest string, nPage int) string {
	url := fmt.Sprintf("/v2/%s/_oras/artifacts/referrers?digest=%s&artifactType=%s&n=%d&nextToken=%s",
		repoName,
		subjectDigest,
		artifactType,
		nPage,
		lastDigest)
	return fmt.Sprintf("<%s>; rel=\"next\"", url)
}
