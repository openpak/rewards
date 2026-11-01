-- Ladder metrics, event ingest, anti-abuse caps and the (inactive) ladder missions.
ALTER TABLE missions DROP CONSTRAINT IF EXISTS missions_metric_check;

-- Metric registry: what servers may count, how, and the daily cap that triggers review.
CREATE TABLE metrics (
    id             TEXT PRIMARY KEY,
    family         TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL,
    mode           TEXT NOT NULL CHECK (mode IN ('sum', 'distinct')),
    daily_cap      BIGINT NOT NULL CHECK (daily_cap > 0),
    max_delta      INT NOT NULL DEFAULT 1 CHECK (max_delta > 0),
    require_friend BOOLEAN NOT NULL DEFAULT false,
    enabled        BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE metric_counts (
    account_id TEXT NOT NULL,
    metric     TEXT NOT NULL,
    total      BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, metric)
);

CREATE TABLE metric_daily (
    account_id TEXT NOT NULL,
    metric     TEXT NOT NULL,
    day        DATE NOT NULL,
    counted    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, metric, day)
);

-- Distinct metrics count each key once per account.
CREATE TABLE metric_keys (
    account_id TEXT NOT NULL,
    metric     TEXT NOT NULL,
    key        TEXT NOT NULL,
    PRIMARY KEY (account_id, metric, key)
);

-- Every accepted event; event_id is the dedupe key. state: counted, held (over the daily cap,
-- waiting for an admin), ignored (frozen or paused account).
CREATE TABLE events (
    event_id    TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    account_id  TEXT NOT NULL,
    metric      TEXT NOT NULL,
    delta       INT NOT NULL,
    keys        TEXT[] NOT NULL DEFAULT '{}',
    state       TEXT NOT NULL CHECK (state IN ('counted', 'held', 'ignored')),
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_held ON events (account_id, metric) WHERE state = 'held';
CREATE INDEX events_received ON events (received_at);


INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('kart_races_completed.mkds','kart_races_completed','Mario Kart DS races completed','sum',150,1,false),
 ('kart_races_completed.mkwii','kart_races_completed','Mario Kart Wii races completed','sum',150,1,false),
 ('kart_races_completed.mk7','kart_races_completed','Mario Kart 7 races completed','sum',150,1,false),
 ('kart_races_completed.mk8','kart_races_completed','Mario Kart 8 races completed','sum',150,1,false),
 ('kart_races_completed.mk8dx','kart_races_completed','Mario Kart 8 Deluxe races completed','sum',150,1,false),
 ('splatoon_matches_completed.s1','splatoon_matches_completed','Splatoon matches completed','sum',100,1,false),
 ('splatoon_matches_completed.s2','splatoon_matches_completed','Splatoon 2 matches completed','sum',100,1,false),
 ('splatoon_matches_completed.s3','splatoon_matches_completed','Splatoon 3 matches completed','sum',100,1,false),
 ('smash_battles_completed.3ds','smash_battles_completed','Smash 3DS battles completed','sum',100,1,false),
 ('smash_battles_completed.wiiu','smash_battles_completed','Smash Wii U battles completed','sum',100,1,false),
 ('smash_battles_completed.ult','smash_battles_completed','Smash Ultimate battles completed','sum',100,1,false),
 ('friends_played_with','','Friends played with','distinct',20,1,true),
 ('maker_courses_played','','Maker courses played','distinct',200,1,false),
 ('maker_courses_uploaded','','Maker courses uploaded','distinct',20,1,false),
 ('trades_completed','','Trades completed','sum',40,1,false),
 ('splatfests_joined','','Splatfests joined','distinct',3,1,false),
 ('kart_tournaments_entered','','Kart tournaments entered','distinct',10,1,false);
INSERT INTO missions (id,title,description,kind,metric,target,reward_points,sort,requires,active) VALUES
 ('kart-races-1','Kart Races: 1','1 online races finished across all Mario Kart games.','series','kart_races_completed',1,2,1001,NULL,false),
 ('kart-races-10','Kart Races: 10','10 online races finished across all Mario Kart games.','series','kart_races_completed',10,5,1002,'kart-races-1',false),
 ('kart-races-25','Kart Races: 25','25 online races finished across all Mario Kart games.','series','kart_races_completed',25,10,1003,'kart-races-10',false),
 ('kart-races-50','Kart Races: 50','50 online races finished across all Mario Kart games.','series','kart_races_completed',50,15,1004,'kart-races-25',false),
 ('kart-races-100','Kart Races: 100','100 online races finished across all Mario Kart games.','series','kart_races_completed',100,25,1005,'kart-races-50',false),
 ('kart-races-500','Kart Races: 500','500 online races finished across all Mario Kart games.','series','kart_races_completed',500,50,1006,'kart-races-100',false),
 ('kart-races-1000','Kart Races: 1,000','1,000 online races finished across all Mario Kart games.','series','kart_races_completed',1000,75,1007,'kart-races-500',false),
 ('kart-races-2500','Kart Races: 2,500','2,500 online races finished across all Mario Kart games.','series','kart_races_completed',2500,100,1008,'kart-races-1000',false),
 ('kart-races-10000','Kart Races: 10,000','10,000 online races finished across all Mario Kart games.','series','kart_races_completed',10000,200,1009,'kart-races-2500',false),
 ('splatoon-matches-1','Splatoon Matches: 1','1 online matches finished across all Splatoon games.','series','splatoon_matches_completed',1,2,1010,NULL,false),
 ('splatoon-matches-10','Splatoon Matches: 10','10 online matches finished across all Splatoon games.','series','splatoon_matches_completed',10,5,1011,'splatoon-matches-1',false),
 ('splatoon-matches-25','Splatoon Matches: 25','25 online matches finished across all Splatoon games.','series','splatoon_matches_completed',25,10,1012,'splatoon-matches-10',false),
 ('splatoon-matches-50','Splatoon Matches: 50','50 online matches finished across all Splatoon games.','series','splatoon_matches_completed',50,15,1013,'splatoon-matches-25',false),
 ('splatoon-matches-100','Splatoon Matches: 100','100 online matches finished across all Splatoon games.','series','splatoon_matches_completed',100,25,1014,'splatoon-matches-50',false),
 ('splatoon-matches-500','Splatoon Matches: 500','500 online matches finished across all Splatoon games.','series','splatoon_matches_completed',500,50,1015,'splatoon-matches-100',false),
 ('splatoon-matches-1000','Splatoon Matches: 1,000','1,000 online matches finished across all Splatoon games.','series','splatoon_matches_completed',1000,75,1016,'splatoon-matches-500',false),
 ('splatoon-matches-2500','Splatoon Matches: 2,500','2,500 online matches finished across all Splatoon games.','series','splatoon_matches_completed',2500,100,1017,'splatoon-matches-1000',false),
 ('splatoon-matches-10000','Splatoon Matches: 10,000','10,000 online matches finished across all Splatoon games.','series','splatoon_matches_completed',10000,200,1018,'splatoon-matches-2500',false),
 ('smash-battles-1','Smash Battles: 1','1 online battles finished across all Smash games.','series','smash_battles_completed',1,2,1019,NULL,false),
 ('smash-battles-10','Smash Battles: 10','10 online battles finished across all Smash games.','series','smash_battles_completed',10,5,1020,'smash-battles-1',false),
 ('smash-battles-25','Smash Battles: 25','25 online battles finished across all Smash games.','series','smash_battles_completed',25,10,1021,'smash-battles-10',false),
 ('smash-battles-50','Smash Battles: 50','50 online battles finished across all Smash games.','series','smash_battles_completed',50,15,1022,'smash-battles-25',false),
 ('smash-battles-100','Smash Battles: 100','100 online battles finished across all Smash games.','series','smash_battles_completed',100,25,1023,'smash-battles-50',false),
 ('smash-battles-500','Smash Battles: 500','500 online battles finished across all Smash games.','series','smash_battles_completed',500,50,1024,'smash-battles-100',false),
 ('smash-battles-1000','Smash Battles: 1,000','1,000 online battles finished across all Smash games.','series','smash_battles_completed',1000,75,1025,'smash-battles-500',false),
 ('smash-battles-2500','Smash Battles: 2,500','2,500 online battles finished across all Smash games.','series','smash_battles_completed',2500,100,1026,'smash-battles-1000',false),
 ('smash-battles-10000','Smash Battles: 10,000','10,000 online battles finished across all Smash games.','series','smash_battles_completed',10000,200,1027,'smash-battles-2500',false),
 ('online-sessions-1','Online Sessions: 1','1 online sessions played in any game.','series','sessions',1,2,1028,NULL,false),
 ('online-sessions-10','Online Sessions: 10','10 online sessions played in any game.','series','sessions',10,5,1029,'online-sessions-1',false),
 ('online-sessions-25','Online Sessions: 25','25 online sessions played in any game.','series','sessions',25,10,1030,'online-sessions-10',false),
 ('online-sessions-50','Online Sessions: 50','50 online sessions played in any game.','series','sessions',50,15,1031,'online-sessions-25',false),
 ('online-sessions-100','Online Sessions: 100','100 online sessions played in any game.','series','sessions',100,25,1032,'online-sessions-50',false),
 ('online-sessions-500','Online Sessions: 500','500 online sessions played in any game.','series','sessions',500,50,1033,'online-sessions-100',false),
 ('online-sessions-1000','Online Sessions: 1,000','1,000 online sessions played in any game.','series','sessions',1000,75,1034,'online-sessions-500',false),
 ('online-sessions-2500','Online Sessions: 2,500','2,500 online sessions played in any game.','series','sessions',2500,100,1035,'online-sessions-1000',false),
 ('online-sessions-10000','Online Sessions: 10,000','10,000 online sessions played in any game.','series','sessions',10000,200,1036,'online-sessions-2500',false),
 ('friends-played-1','Friends Played With: 1','1 different friends played with online.','series','friends_played_with',1,2,1037,NULL,false),
 ('friends-played-5','Friends Played With: 5','5 different friends played with online.','series','friends_played_with',5,3,1038,'friends-played-1',false),
 ('friends-played-10','Friends Played With: 10','10 different friends played with online.','series','friends_played_with',10,5,1039,'friends-played-5',false),
 ('friends-played-25','Friends Played With: 25','25 different friends played with online.','series','friends_played_with',25,10,1040,'friends-played-10',false),
 ('friends-played-50','Friends Played With: 50','50 different friends played with online.','series','friends_played_with',50,15,1041,'friends-played-25',false),
 ('friends-played-100','Friends Played With: 100','100 different friends played with online.','series','friends_played_with',100,25,1042,'friends-played-50',false),
 ('friends-played-250','Friends Played With: 250','250 different friends played with online.','series','friends_played_with',250,30,1043,'friends-played-100',false),
 ('maker-played-1','Courses Played: 1','1 different Maker courses played.','series','maker_courses_played',1,2,1044,NULL,false),
 ('maker-played-10','Courses Played: 10','10 different Maker courses played.','series','maker_courses_played',10,5,1045,'maker-played-1',false),
 ('maker-played-25','Courses Played: 25','25 different Maker courses played.','series','maker_courses_played',25,10,1046,'maker-played-10',false),
 ('maker-played-50','Courses Played: 50','50 different Maker courses played.','series','maker_courses_played',50,15,1047,'maker-played-25',false),
 ('maker-played-100','Courses Played: 100','100 different Maker courses played.','series','maker_courses_played',100,25,1048,'maker-played-50',false),
 ('maker-played-500','Courses Played: 500','500 different Maker courses played.','series','maker_courses_played',500,50,1049,'maker-played-100',false),
 ('maker-played-1000','Courses Played: 1,000','1,000 different Maker courses played.','series','maker_courses_played',1000,75,1050,'maker-played-500',false),
 ('maker-played-2500','Courses Played: 2,500','2,500 different Maker courses played.','series','maker_courses_played',2500,100,1051,'maker-played-1000',false),
 ('maker-uploaded-1','Courses Uploaded: 1','1 Maker courses uploaded.','series','maker_courses_uploaded',1,2,1052,NULL,false),
 ('maker-uploaded-5','Courses Uploaded: 5','5 Maker courses uploaded.','series','maker_courses_uploaded',5,3,1053,'maker-uploaded-1',false),
 ('maker-uploaded-10','Courses Uploaded: 10','10 Maker courses uploaded.','series','maker_courses_uploaded',10,5,1054,'maker-uploaded-5',false),
 ('maker-uploaded-25','Courses Uploaded: 25','25 Maker courses uploaded.','series','maker_courses_uploaded',25,10,1055,'maker-uploaded-10',false),
 ('maker-uploaded-50','Courses Uploaded: 50','50 Maker courses uploaded.','series','maker_courses_uploaded',50,15,1056,'maker-uploaded-25',false),
 ('maker-uploaded-100','Courses Uploaded: 100','100 Maker courses uploaded.','series','maker_courses_uploaded',100,25,1057,'maker-uploaded-50',false),
 ('maker-uploaded-250','Courses Uploaded: 250','250 Maker courses uploaded.','series','maker_courses_uploaded',250,30,1058,'maker-uploaded-100',false),
 ('maker-uploaded-500','Courses Uploaded: 500','500 Maker courses uploaded.','series','maker_courses_uploaded',500,50,1059,'maker-uploaded-250',false),
 ('trades-1','Trades: 1','1 trades completed.','series','trades_completed',1,2,1060,NULL,false),
 ('trades-10','Trades: 10','10 trades completed.','series','trades_completed',10,5,1061,'trades-1',false),
 ('trades-25','Trades: 25','25 trades completed.','series','trades_completed',25,10,1062,'trades-10',false),
 ('trades-50','Trades: 50','50 trades completed.','series','trades_completed',50,15,1063,'trades-25',false),
 ('trades-100','Trades: 100','100 trades completed.','series','trades_completed',100,25,1064,'trades-50',false),
 ('trades-500','Trades: 500','500 trades completed.','series','trades_completed',500,50,1065,'trades-100',false),
 ('trades-1000','Trades: 1,000','1,000 trades completed.','series','trades_completed',1000,75,1066,'trades-500',false),
 ('splatfests-1','Splatfests Joined: 1','1 Splatfests joined.','series','splatfests_joined',1,2,1067,NULL,false),
 ('splatfests-5','Splatfests Joined: 5','5 Splatfests joined.','series','splatfests_joined',5,3,1068,'splatfests-1',false),
 ('splatfests-10','Splatfests Joined: 10','10 Splatfests joined.','series','splatfests_joined',10,5,1069,'splatfests-5',false),
 ('splatfests-25','Splatfests Joined: 25','25 Splatfests joined.','series','splatfests_joined',25,10,1070,'splatfests-10',false),
 ('splatfests-50','Splatfests Joined: 50','50 Splatfests joined.','series','splatfests_joined',50,15,1071,'splatfests-25',false),
 ('kart-tournaments-1','Kart Tournaments: 1','1 Kart tournaments entered.','series','kart_tournaments_entered',1,2,1072,NULL,false),
 ('kart-tournaments-5','Kart Tournaments: 5','5 Kart tournaments entered.','series','kart_tournaments_entered',5,3,1073,'kart-tournaments-1',false),
 ('kart-tournaments-10','Kart Tournaments: 10','10 Kart tournaments entered.','series','kart_tournaments_entered',10,5,1074,'kart-tournaments-5',false),
 ('kart-tournaments-25','Kart Tournaments: 25','25 Kart tournaments entered.','series','kart_tournaments_entered',25,10,1075,'kart-tournaments-10',false),
 ('kart-tournaments-50','Kart Tournaments: 50','50 Kart tournaments entered.','series','kart_tournaments_entered',50,15,1076,'kart-tournaments-25',false),
 ('kart-tournaments-100','Kart Tournaments: 100','100 Kart tournaments entered.','series','kart_tournaments_entered',100,25,1077,'kart-tournaments-50',false),
 ('kart-first-mkds','First finish: Mario Kart DS','Finish one online game in Mario Kart DS.','once','kart_races_completed.mkds',1,5,1078,NULL,false),
 ('kart-first-mkwii','First finish: Mario Kart Wii','Finish one online game in Mario Kart Wii.','once','kart_races_completed.mkwii',1,5,1079,NULL,false),
 ('kart-first-mk7','First finish: Mario Kart 7','Finish one online game in Mario Kart 7.','once','kart_races_completed.mk7',1,5,1080,NULL,false),
 ('kart-first-mk8','First finish: Mario Kart 8','Finish one online game in Mario Kart 8.','once','kart_races_completed.mk8',1,5,1081,NULL,false),
 ('kart-first-mk8dx','First finish: Mario Kart 8 Deluxe','Finish one online game in Mario Kart 8 Deluxe.','once','kart_races_completed.mk8dx',1,5,1082,NULL,false),
 ('kart-collector','Kart Collector','Finish an online game in each of: Mario Kart DS, Mario Kart Wii, Mario Kart 7, Mario Kart 8, Mario Kart 8 Deluxe.','once','family_games:kart_races_completed',5,30,1083,NULL,false),
 ('splatoon-first-s1','First finish: Splatoon','Finish one online game in Splatoon.','once','splatoon_matches_completed.s1',1,5,1084,NULL,false),
 ('splatoon-first-s2','First finish: Splatoon 2','Finish one online game in Splatoon 2.','once','splatoon_matches_completed.s2',1,5,1085,NULL,false),
 ('splatoon-first-s3','First finish: Splatoon 3','Finish one online game in Splatoon 3.','once','splatoon_matches_completed.s3',1,5,1086,NULL,false),
 ('splatoon-collector','Splatoon Collector','Finish an online game in each of: Splatoon, Splatoon 2, Splatoon 3.','once','family_games:splatoon_matches_completed',3,30,1087,NULL,false),
 ('smash-first-3ds','First finish: Smash 3DS','Finish one online game in Smash 3DS.','once','smash_battles_completed.3ds',1,5,1088,NULL,false),
 ('smash-first-wiiu','First finish: Smash Wii U','Finish one online game in Smash Wii U.','once','smash_battles_completed.wiiu',1,5,1089,NULL,false),
 ('smash-first-ult','First finish: Smash Ultimate','Finish one online game in Smash Ultimate.','once','smash_battles_completed.ult',1,5,1090,NULL,false),
 ('smash-collector','Smash Collector','Finish an online game in each of: Smash 3DS, Smash Wii U, Smash Ultimate.','once','family_games:smash_battles_completed',3,30,1091,NULL,false);
