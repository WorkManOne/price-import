package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/WorkManOne/price-import/api"
)

// все ручки из server.go должны быть описаны в openapi, а то спека разъедется с кодом
func TestOpenAPICoversRoutes(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PATCH|PUT|DELETE) (/api/[^"]+|/healthz)"`)
	spec := string(api.Spec)
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		found++
		method, path := strings.ToLower(m[1]), m[2]
		i := strings.Index(spec, "\n  "+path+":\n")
		if i < 0 {
			t.Errorf("в openapi нет пути %s", path)
			continue
		}
		// метод должен быть в блоке этого пути (до следующего пути)
		block := spec[i+1:]
		if j := regexp.MustCompile(`\n  /`).FindStringIndex(block[1:]); j != nil {
			block = block[:j[0]+1]
		}
		if !strings.Contains(block, "\n    "+method+":") {
			t.Errorf("в openapi нет %s %s", m[1], path)
		}
	}
	if found < 10 {
		t.Fatalf("нашли всего %d ручек, регулярка сломалась?", found)
	}
}
