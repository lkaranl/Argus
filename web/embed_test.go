package web

import (
	"io/fs"
	"testing"
)

func TestEmbeddedStaticFiles(t *testing.T) {
	staticFS, err := StaticFS()
	if err != nil {
		t.Fatalf("Erro ao obter StaticFS: %v", err)
	}

	requiredFiles := []string{
		"index.html",
		"css/style.css",
		"js/clock.js",
		"js/telemetry.js",
		"js/webrtc.js",
	}

	for _, relPath := range requiredFiles {
		file, err := staticFS.Open(relPath)
		if err != nil {
			t.Errorf("Arquivo obrigatório não encontrado no embed: %s (%v)", relPath, err)
			continue
		}

		info, err := file.Stat()
		_ = file.Close()
		if err != nil {
			t.Errorf("Erro ao ler stat de %s: %v", relPath, err)
			continue
		}

		if info.Size() == 0 {
			t.Errorf("Arquivo embutido %s está vazio (0 bytes)", relPath)
		}
	}

	// Testa leitura de diretório
	entries, err := fs.ReadDir(staticFS, ".")
	if err != nil {
		t.Fatalf("Erro ao listar raiz de staticFS: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("Nenhum arquivo encontrado na raiz do StaticFS")
	}
}
