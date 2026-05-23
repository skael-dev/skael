package skill

import (
	"context"
	"fmt"
)

// Search returns skills matching the given query using a combination of
// Postgres full-text search (websearch_to_tsquery + ts_rank on search_vector)
// and pg_trgm fuzzy name matching (similarity()). Results are returned where
// either FTS matches OR name similarity exceeds 0.2, ordered by FTS rank then
// trigram rank. Limit caps the number of returned results.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Skill, error) {
	const q = `
		SELECT id, name, display_name, description, '', latest_version, frontmatter, created_at, updated_at,
		    ts_rank(search_vector, websearch_to_tsquery('english', $1)) AS fts_rank,
		    similarity(name, $1) AS trgm_rank
		FROM skills
		WHERE search_vector @@ websearch_to_tsquery('english', $1)
		    OR similarity(name, $1) > 0.2
		ORDER BY fts_rank DESC, trgm_rank DESC
		LIMIT $2
	`
	rows, err := s.pool.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Search query: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var sk Skill
		var rawFrontmatter []byte
		var ftsRank, trgmRank float64
		err := rows.Scan(
			&sk.ID,
			&sk.Name,
			&sk.DisplayName,
			&sk.Description,
			&sk.Content,
			&sk.LatestVersion,
			&rawFrontmatter,
			&sk.CreatedAt,
			&sk.UpdatedAt,
			&ftsRank,
			&trgmRank,
		)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Search scan: %w", err)
		}
		sk.Frontmatter = rawFrontmatter
		skills = append(skills, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.Search rows: %w", err)
	}
	return skills, nil
}
