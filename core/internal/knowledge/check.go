package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/embedmodel"
)

const (
	checkTable      = "astrlink_self_check"
	checkIndexWait  = 5 * time.Second
	checkIndexRetry = 100 * time.Millisecond
)

// CheckRuntime runs full-text, vector and hybrid queries on a scratch table
// and drops it, so a user can see the search engine works on this device.
func (environment *Environment) CheckRuntime(ctx context.Context) (contract.KnowledgeRuntimeCheck, error) {
	db, err := environment.DB()
	if err != nil {
		return contract.KnowledgeRuntimeCheck{}, err
	}
	check := contract.KnowledgeRuntimeCheck{OK: true}
	step := func(name string, run func() (string, error)) bool {
		started := time.Now()
		detail, err := run()
		entry := contract.KnowledgeCheckStep{Name: name, OK: err == nil, DurationMS: time.Since(started).Milliseconds(), Detail: detail}
		if err != nil {
			entry.Detail = err.Error()
			check.OK = false
		}
		check.Steps = append(check.Steps, entry)
		return err == nil
	}
	// The vector index catches up with writes asynchronously, a few hundred
	// milliseconds behind them, so a query that finds nothing is retried.
	expect := func(query string, want int) func() (string, error) {
		return func() (string, error) {
			started := time.Now()
			for {
				var id int
				err := db.QueryRowContext(ctx, query).Scan(&id)
				if err == nil && id != want {
					return "", fmt.Errorf("found row %d, expected %d", id, want)
				}
				if err == nil {
					if waited := time.Since(started); waited > checkIndexRetry {
						return fmt.Sprintf("index_wait_ms=%d", waited.Milliseconds()), nil
					}
					return "", nil
				}
				if !errors.Is(err, sql.ErrNoRows) || time.Since(started) > checkIndexWait {
					return "", err
				}
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(checkIndexRetry):
				}
			}
		}
	}
	if !step("connect", func() (string, error) {
		return "", db.QueryRowContext(ctx, "SELECT version()").Scan(&check.Engine)
	}) {
		return check, nil
	}
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+checkTable)
	defer func() { _, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS "+checkTable) }()
	if !step("create", exec(ctx, db, `CREATE TABLE `+checkTable+` (
    id BIGINT PRIMARY KEY,
    content TEXT NOT NULL,
    embedding VECTOR(4),
    FULLTEXT INDEX ft (content) WITH PARSER ik,
    VECTOR INDEX vi (embedding) WITH (DISTANCE=l2, TYPE=hnsw, LIB=vsag)
) ORGANIZATION = HEAP`)) {
		return check, nil
	}
	if !step("write", exec(ctx, db, `INSERT INTO `+checkTable+` (id, content, embedding) VALUES
    (1, '在 Windows 上部署时防火墙拦截了端口', '[1,0,0,0]'),
    (2, 'macOS 打包后需要签名和公证', '[0,1,0,0]'),
    (3, 'The deadlock came from a channel without a receiver', '[0,0,1,0]'),
    (4, '用 Docker 部署数据库并配置时区', '[0.6,0,0,0.8]')`)) {
		return check, nil
	}
	step("full_text", expect(`SELECT id FROM `+checkTable+` WHERE MATCH(content) AGAINST('防火墙') ORDER BY MATCH(content) AGAINST('防火墙') DESC LIMIT 1`, 1))
	step("vector", expect(`SELECT id FROM `+checkTable+` ORDER BY l2_distance(embedding, '[0.1,0.9,0,0]') APPROXIMATE LIMIT 1`, 2))
	step("hybrid", expect(`SELECT id FROM `+checkTable+` WHERE MATCH(content) AGAINST('部署') ORDER BY l2_distance(embedding, '[0.5,0,0,0.9]') APPROXIMATE LIMIT 1`, 4))
	return check, nil
}

func exec(ctx context.Context, db *sql.DB, statement string) func() (string, error) {
	return func() (string, error) {
		_, err := db.ExecContext(ctx, statement)
		return "", err
	}
}

// checkTexts pair a Chinese question with its English paraphrase, then with
// unrelated text, so the similarities show whether the model works.
var checkTexts = []string{
	"怎么在 Windows 防火墙里放行一个端口",
	"How do I open a port in the Windows firewall?",
	"macOS 上的应用需要签名和公证",
	"今天中午吃什么",
}

// CheckModel loads a local model, embeds sample texts and reports the
// similarity of related and unrelated pairs.
func (environment *Environment) CheckModel(ctx context.Context, id string) (contract.KnowledgeEmbeddingCheck, error) {
	model, ok := embedmodel.Lookup(id)
	if !ok {
		return contract.KnowledgeEmbeddingCheck{}, ErrUnknownModel
	}
	check := contract.KnowledgeEmbeddingCheck{Dimensions: model.Dimensions}
	loaded := environment.worker.Running()
	started := time.Now()
	if _, err := environment.Embed(ctx, id, embedmodel.Query, checkTexts[:1]); err != nil {
		return check, err
	}
	if !loaded {
		check.LoadMS = time.Since(started).Milliseconds()
	}
	started = time.Now()
	vectors, err := environment.Embed(ctx, id, embedmodel.Passage, checkTexts)
	if err != nil {
		return check, err
	}
	check.EmbedMS = time.Since(started).Milliseconds()
	for _, right := range []int{1, 2, 3} {
		check.Pairs = append(check.Pairs, contract.KnowledgeEmbeddingPair{
			Left: checkTexts[0], Right: checkTexts[right], Similarity: cosine(vectors[0], vectors[right]),
		})
	}
	return check, nil
}

func cosine(left, right []float32) float64 {
	var dot, leftNorm, rightNorm float64
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
		leftNorm += float64(left[index]) * float64(left[index])
		rightNorm += float64(right[index]) * float64(right[index])
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return math.Round(dot/math.Sqrt(leftNorm*rightNorm)*1000) / 1000
}
