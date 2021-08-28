package rotate

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestRotateWriter_NewRotateWriter(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	want := struct {
		maxSize    int64
		keepDays   int64
		delimiter  string
		timeFormat string
		gzip       bool
		localTime  bool
		maxBackups int64
	}{
		maxSize:    128,
		keepDays:   30,
		delimiter:  "-",
		timeFormat: defaultTimeFormat,
		gzip:       true,
		localTime:  true,
		maxBackups: 30,
	}

	writer, err := NewRotateWriter(
		tmpFileName,
		WithMaxSize(want.maxSize),
		WithMaxDays(want.keepDays),
		WithDelimiter(want.delimiter),
		WithTimeFormat(want.timeFormat),
		WithGzip(want.gzip),
		WithLocalTime(want.localTime),
		WithMaxBackups(want.maxBackups),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("test")); err !=nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWriter_Write(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("write without rotate", func(t *testing.T) {
		writer, err := NewRotateWriter(tmpFileName)
		if err != nil {
			t.Fatal(err)
		}
		//backupName := writer.backupName
		if n, err := writer.Write([]byte("test")); err != nil {
			t.Fatal(err)
		} else if writer.size != int64(n) {
			t.Errorf("writing writer size incorrect")
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("write with rotate and gzip", func(t *testing.T) {
		writer, err := NewRotateWriter(tmpFileName, WithGzip(true))
		if err != nil {
			t.Fatal(err)
		}
		backupName := writer.backupName

		oversize := make([]byte, writer.opt.maxSize)
		if _, err := writer.Write(oversize); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("test")); err != nil {
			t.Fatal(err)
		}else if err = writer.Close(); err != nil {
			t.Fatal(err)
		}

		if err := os.Remove(backupName); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50*time.Millisecond)
		if writer.opt.gzip  {
			backupName += ".gz"
		}
		if err := os.Remove(backupName); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRotateWriter_Close(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName)
	if err != nil {
		t.Fatal(err)
	} else if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWriter_rotate(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName)
	if err != nil {
		t.Fatal(err)
	}
	backupName := writer.backupName

	if err := writer.rotate(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(backupName); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWriter_compressFile(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName, WithGzip(true))
	if err != nil {
		t.Fatal(err)
	}
	writer.compressFile(tmpFileName)
	if writer.err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tmpFileName + ".gz"); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWriter_deleteOutdatedFiles(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName, WithMaxDays(30))
	if err != nil {
		t.Fatal(err)
	}

	tDate := time.Now().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - 24*time.Hour).Format(writer.opt.timeFormat)
	if !writer.opt.localTime {
		tDate = time.Now().UTC().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - 24*time.Hour).Format(writer.opt.timeFormat)
	}
	wantName := mockBackupName(writer.filename, tDate)
	if fp, err := os.Create(wantName); err != nil {
		t.Fatal(err)
	} else if err := fp.Close(); err != nil {
		t.Fatal(err)
	}

	writer.deleteOutdatedFiles()
	if writer.err != nil {
		t.Fatal(writer.err)
	}

	if _, err := os.Stat(wantName); os.IsExist(err) {
		t.Fatalf("not delete %s", wantName)
	}
}


func TestRotateWriter_deleteOverMaxFiles(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	maxBackups := 5
	writer, err := NewRotateWriter(tmpFileName, WithMaxBackups(int64(maxBackups)))
	if err != nil {
		t.Fatal(err)
	}

	wantFiles := make([]string, 0)
	for i := 0; i < 6; i++ {
		dur := 24 * time.Hour * time.Duration(i)
		tDate := time.Now().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - dur).Format(writer.opt.timeFormat)
		if !writer.opt.localTime {
			tDate = time.Now().UTC().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - dur).Format(writer.opt.timeFormat)
		}
		wantName := mockBackupName(writer.filename, tDate)
		if fp, err := os.Create(wantName); err != nil {
			t.Fatal(err)
		} else if err := fp.Close(); err != nil {
			t.Fatal(err)
		}
		wantFiles = append(wantFiles, wantName)
	}
	sort.Strings(wantFiles)
	wantFiles = wantFiles[len(wantFiles)-maxBackups:]

	writer.deleteOverMaxFiles()
	if writer.err != nil {
		t.Fatal(writer.err)
	}

	gotFiles, err := writer.listFiles()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(gotFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("delete over max file incorrect")
	}

	for _, got := range gotFiles {
		if err := os.Remove(got); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRotateWriter_backupFileName(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName, WithTimeFormat("2006-01-02T15:04:05"))
	if err != nil {
		t.Fatal(err)
	}

	wantName := mockBackupName(tmpFileName, nowDate(writer.opt.timeFormat, writer.opt.localTime))
	gotName := writer.backupFileName()
	if wantName != gotName {
		t.Errorf("backupName incorrect, got:%v, want:%v", gotName, wantName)
	}
}

func TestRotateWriter_gzipFile(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()

	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gzipFile(tmpFileName); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(tmpFileName + ".gz"); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWriter_oldFiles(t *testing.T) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "temp.log")
	if err != nil {
		t.Fatal(err)
	}
	tmpFileName := tmpFile.Name()
	defer func(t *testing.T) {
		if err := os.Remove(tmpFileName); err != nil {
			t.Fatal(err)
		}
	}(t)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewRotateWriter(tmpFileName, WithMaxDays(30))
	if err != nil {
		t.Fatal(err)
	}

	tDate := time.Now().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - 24*time.Hour).Format(writer.opt.timeFormat)
	if !writer.opt.localTime {
		tDate = time.Now().UTC().Add(-time.Hour*time.Duration(24*writer.opt.maxDays) - 24*time.Hour).Format(writer.opt.timeFormat)
	}
	wantName := mockBackupName(writer.filename, tDate)
	if fp, err := os.Create(wantName); err != nil {
		t.Fatal(err)
	} else if err := fp.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := writer.listFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 && files[0] != wantName {
		t.Fatalf("old filename incorrect")
	}

	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
}

func mockBackupName(name string, date string) string {
	ext := filepath.Ext(name)
	prefix := name[:len(name)-len(ext)]
	delimiter := defaultDelimiter
	wantName := fmt.Sprintf("%s%s%s%s", prefix, delimiter, date, ext)
	return wantName
}
