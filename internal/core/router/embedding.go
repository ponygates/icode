// Local semantic routing — a fully offline, zero-API, zero-token complexity
// classifier that upgrades routing accuracy over the pure keyword heuristic.
//
// Why local hashing instead of a real embedding API? Calling an embedding
// endpoint on every user turn would burn tokens/latency on the very hot path
// we are trying to keep cheap — that directly contradicts iCode's core
// "save tokens" mission. Instead we build a small bag-of-features vector
// (word tokens + CJK bigrams, hashed and TF-weighted, L2-normalized) and
// compare it by cosine similarity against a handful of labeled exemplars per
// complexity class. This is deterministic, instant, offline, and free, while
// still being far more robust than substring keyword matching (it degrades
// gracefully on paraphrases and mixed zh/en input).
//
// Enable with config routing.mode: "embedding".
package router

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// SemanticClassifier grades query complexity by nearest-centroid cosine
// similarity over local hashed feature vectors. It never calls the network.
type SemanticClassifier struct {
	// centroids[c] is the mean L2-normalized feature vector for class c.
	centroids map[Complexity]map[uint64]float64
	// absThreshold is the minimum top cosine score required to trust the
	// semantic verdict; below it we defer to the keyword heuristic.
	absThreshold float64
	// margin is the minimum gap between the best and second-best class
	// scores required to trust the verdict (avoids coin-flip decisions).
	margin float64
}

// exemplars are short, representative queries for each complexity class,
// covering both English and Chinese phrasings. Keep them distinctive: the
// classifier generalizes from these, so overlap between classes hurts.
var exemplars = map[Complexity][]string{
	ComplexitySimple: {
		"what is a goroutine",
		"explain what this function does",
		"how do I list files in bash",
		"what does this error mean",
		"什么是闭包",
		"这个报错是什么意思",
		"解释一下这段代码",
		"go 语言怎么读取环境变量",
		"这个参数有什么用",
		"帮我看看这是什么",
	},
	ComplexityNormal: {
		"write a function to parse json",
		"add a unit test for this handler",
		"fix the nil pointer bug in login",
		"implement a retry wrapper for http calls",
		"rename this variable across the file",
		"给这个接口加一个字段",
		"帮我写一个快速排序",
		"修复登录时的空指针问题",
		"给这个函数补一个测试",
		"实现一个简单的缓存",
	},
	ComplexityComplex: {
		"refactor the whole authentication module and split it into packages",
		"redesign the architecture to support multiple providers",
		"migrate the storage layer from sqlite to postgres across the project",
		"do a deep analysis of the concurrency model and optimize the hot path",
		"review all files and propose a performance benchmark suite",
		"重构整个鉴权模块并拆分成多个包",
		"重新设计架构以支持多提供商",
		"把整个项目的存储层从 sqlite 迁移到 postgres",
		"深度分析并发模型并优化性能热点",
		"全面审查所有代码并给出基准测试方案",
	},
}

// NewSemanticClassifier builds the classifier from the built-in exemplars.
// The result is immutable and safe for concurrent use.
func NewSemanticClassifier() *SemanticClassifier {
	sc := &SemanticClassifier{
		centroids:    make(map[Complexity]map[uint64]float64, len(exemplars)),
		absThreshold: 0.06,
		margin:       0.02,
	}
	for class, samples := range exemplars {
		acc := map[uint64]float64{}
		for _, s := range samples {
			v := featurize(s)
			for k, val := range v {
				acc[k] += val
			}
		}
		// Mean, then L2-normalize the centroid so cosine == dot product.
		if n := float64(len(samples)); n > 0 {
			for k := range acc {
				acc[k] /= n
			}
		}
		normalize(acc)
		sc.centroids[class] = acc
	}
	return sc
}

// Classify returns the best-matching complexity and whether the verdict is
// confident enough to trust. When ok is false the caller should keep its
// existing (keyword) classification.
func (sc *SemanticClassifier) Classify(query string) (Complexity, bool) {
	qv := featurize(query)
	if len(qv) == 0 {
		return ComplexityNormal, false
	}
	normalize(qv)

	best, second := math.Inf(-1), math.Inf(-1)
	bestClass := ComplexityNormal
	for class, cen := range sc.centroids {
		score := dot(qv, cen)
		if score > best {
			second = best
			best = score
			bestClass = class
		} else if score > second {
			second = score
		}
	}

	if best < sc.absThreshold || (best-second) < sc.margin {
		return bestClass, false
	}
	return bestClass, true
}

// featurize turns text into a sparse, TF-weighted feature vector keyed by a
// 64-bit hash of each feature. Features are: lowercased ASCII word tokens and
// adjacent CJK character bigrams (plus CJK unigrams for single-char signals).
func featurize(text string) map[uint64]float64 {
	counts := map[uint64]int{}
	text = strings.ToLower(text)

	var word []rune
	var prevCJK rune
	hasPrevCJK := false

	flushWord := func() {
		if len(word) > 0 {
			counts[hashFeature("w:"+string(word))]++
			word = word[:0]
		}
	}

	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			// CJK unigram.
			counts[hashFeature("c:"+string(r))]++
			// CJK bigram with the previous CJK rune.
			if hasPrevCJK {
				counts[hashFeature("b:"+string(prevCJK)+string(r))]++
			}
			prevCJK = r
			hasPrevCJK = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
			hasPrevCJK = false
		default:
			flushWord()
			hasPrevCJK = false
		}
	}
	flushWord()

	// TF weighting: 1 + log(count) dampens repeated tokens.
	vec := make(map[uint64]float64, len(counts))
	for k, c := range counts {
		vec[k] = 1 + math.Log(float64(c))
	}
	return vec
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func hashFeature(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// normalize scales the vector to unit L2 length in place.
func normalize(v map[uint64]float64) {
	var sum float64
	for _, val := range v {
		sum += val * val
	}
	if sum == 0 {
		return
	}
	inv := 1 / math.Sqrt(sum)
	for k := range v {
		v[k] *= inv
	}
}

// dot returns the dot product of two sparse vectors. When both are
// L2-normalized this equals their cosine similarity. Iterating the smaller
// map keeps it cheap.
func dot(a, b map[uint64]float64) float64 {
	if len(b) < len(a) {
		a, b = b, a
	}
	var s float64
	for k, va := range a {
		if vb, ok := b[k]; ok {
			s += va * vb
		}
	}
	return s
}
