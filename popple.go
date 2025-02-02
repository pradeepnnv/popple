package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const DATABASE string = "popple.sqlite"
const DEFAULT_WORKERS uint = 4
const DEFAULT_JOBS uint = 128

func main() {
	tokenFile := flag.String("token", "", "path to file containing bot token")
	numWorkers := flag.Uint("workers", DEFAULT_WORKERS, "Number of worker threads to spawn")
	dbFile := flag.String("db", DATABASE, "Path to database file")
	numJobs := flag.Uint("jobs", DEFAULT_JOBS, "Maximum queue size for jobs")
	flag.Parse()

	if *tokenFile == "" {
		fmt.Fprintf(os.Stderr, "Token file must be supplied as a command line argument")
		os.Exit(1)
	}

	if *numWorkers < 1 {
		*numWorkers = 1
	}

	db, err := gorm.Open(sqlite.Open(*dbFile), &gorm.Config{
		/* FIXME: Might want to tweak this some more. I turned it off because
		it would log when an entry is not found and that's fine, especially for
		the !karma command. */
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %s\n", err)
		os.Exit(1)
	}

	token, err := ioutil.ReadFile(*tokenFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read token from %s\n", *tokenFile)
		os.Exit(1)
	}

	db.AutoMigrate(&Config{})
	db.AutoMigrate(&Entity{})

	session, err := discordgo.New("Bot " + string(token))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize Discord library: %s\n", err)
		os.Exit(1)
	}

	cancel := make(chan struct{})
	workQueue := make(chan Job, *numJobs)

	for i := uint(0); i < *numWorkers; i++ {
		go worker(workQueue, cancel, db)
	}

	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		select {
		case workQueue <- Job{s, m}:
		default:
			fmt.Fprintf(os.Stderr, "Warning: job queue capacity depleted; dropping incoming job\n")
		}
	})

	err = session.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to Discord: %s\n", err)
		/* Should these be `defer`red? */
		close(cancel)
		close(workQueue)
		os.Exit(1)
	}

	fmt.Println("Popple is online")

	session_channel := make(chan os.Signal, 1)
	signal.Notify(session_channel, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-session_channel
	close(cancel)
	close(workQueue)

	session.Close()
}
